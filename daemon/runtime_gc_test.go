package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/types"
	daemonconfig "github.com/AliyunContainerService/terway/types/daemon"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Record patches while retaining real API conflict handling and response decoding.
type runtimeGCClient struct {
	client.Client
	patches     [][]byte
	beforePatch func()
}

type runtimeGCStatusWriter struct {
	client.SubResourceWriter
	owner *runtimeGCClient
}

func (c *runtimeGCClient) Status() client.SubResourceWriter {
	return &runtimeGCStatusWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

func (w *runtimeGCStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	data, err := patch.Data(obj)
	if err != nil {
		return err
	}
	w.owner.patches = append(w.owner.patches, data)
	if f := w.owner.beforePatch; f != nil {
		w.owner.beforePatch = nil
		f()
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func TestRuntimeGCConsistency(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../pkg/apis/crds"}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	initial := func(id string) *networkv1beta1.RuntimePodStatus {
		return &networkv1beta1.RuntimePodStatus{PodID: "default/" + id, Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
			networkv1beta1.CNIStatusInitial: {LastUpdateTime: metav1.NewTime(time.Now().Add(-time.Minute))},
		}}
	}
	create := func(t *testing.T, name string, count int) *networkv1beta1.NodeRuntime {
		t.Helper()
		obj := &networkv1beta1.NodeRuntime{ObjectMeta: metav1.ObjectMeta{Name: name}}
		require.NoError(t, c.Create(ctx, obj))
		obj.Status.Pods = map[string]*networkv1beta1.RuntimePodStatus{}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("pod-%d", i)
			obj.Status.Pods[id] = initial(id)
		}
		require.NoError(t, c.Status().Update(ctx, obj))
		t.Cleanup(func() {
			err := c.Delete(ctx, &networkv1beta1.NodeRuntime{ObjectMeta: metav1.ObjectMeta{Name: name}})
			if !apierrors.IsNotFound(err) {
				require.NoError(t, err)
			}
		})
		return obj
	}
	service := func(obj *networkv1beta1.NodeRuntime, recorded *runtimeGCClient, check func(context.Context, string, string) (bool, error)) *networkService {
		k := &mocks.Kubernetes{}
		k.On("NodeName").Return(obj.Name)
		k.On("GetClient").Return(recorded)
		k.On("PodExist", mock.Anything, mock.Anything, mock.Anything).Return(check)
		return &networkService{k8s: k, ipamType: types.IPAMTypeCRD, daemonMode: daemonconfig.ModeENIMultiIP}
	}
	countDeleted := func(t *testing.T, obj *networkv1beta1.NodeRuntime) int {
		t.Helper()
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
		count := 0
		for _, status := range obj.Status.Pods {
			if status.Status[networkv1beta1.CNIStatusDeleted] != nil {
				count++
			}
		}
		return count
	}
	t.Run("batch boundaries survive real response decoding", func(t *testing.T) {
		obj := create(t, "batch-response", 2*runtimeGCBatchSize+1)
		recorded := &runtimeGCClient{Client: c}
		checks := 0
		ns := service(obj, recorded, func(context.Context, string, string) (bool, error) {
			if checks == runtimeGCBatchSize || checks == 2*runtimeGCBatchSize {
				require.Equal(t, checks, countDeleted(t, obj), "previous batch must persist before more checks")
			}
			checks++
			return false, nil
		})
		require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
		require.Equal(t, 2*runtimeGCBatchSize+1, countDeleted(t, obj))
		require.Len(t, recorded.patches, 3)
		for _, data := range recorded.patches {
			var payload struct {
				Metadata struct {
					ResourceVersion string `json:"resourceVersion"`
				} `json:"metadata"`
				Status struct {
					Pods map[string]struct {
						Status map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo `json:"status"`
					} `json:"pods"`
				} `json:"status"`
			}
			require.NoError(t, json.Unmarshal(data, &payload))
			require.NotEmpty(t, payload.Metadata.ResourceVersion)
			require.LessOrEqual(t, len(payload.Status.Pods), runtimeGCBatchSize)
			require.NotContains(t, string(data), "podID")
			for _, pod := range payload.Status.Pods {
				require.Len(t, pod.Status, 1)
				require.NotNil(t, pod.Status[networkv1beta1.CNIStatusDeleted])
			}
		}
	})
	t.Run("completed batch survives cancellation and remaining work resumes", func(t *testing.T) {
		obj := create(t, "batch-cancel", runtimeGCBatchSize+1)
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		checks := 0
		recorded := &runtimeGCClient{Client: c}
		ns := service(obj, recorded, func(ctx context.Context, _ string, _ string) (bool, error) {
			checks++
			if checks == runtimeGCBatchSize+1 {
				cancel()
				return false, ctx.Err()
			}
			return false, nil
		})
		require.ErrorIs(t, ns.cleanRuntimeNode(canceled, sets.New[string]()), context.Canceled)
		require.Equal(t, runtimeGCBatchSize, countDeleted(t, obj))
		require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
		require.Equal(t, runtimeGCBatchSize+1, countDeleted(t, obj))
	})
	t.Run("query deadline leaves time to save the tail batch", func(t *testing.T) {
		obj := create(t, "batch-deadline", 3)
		deadlineCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		deadline, ok := deadlineCtx.Deadline()
		require.True(t, ok)
		checks := 0
		recorded := &runtimeGCClient{Client: c}
		ns := service(obj, recorded, func(queryCtx context.Context, _ string, _ string) (bool, error) {
			checks++
			if checks == 2 {
				require.Empty(t, recorded.patches, "first result must still be in the tail batch")
				queryDeadline, ok := queryCtx.Deadline()
				require.True(t, ok)
				require.True(t, queryDeadline.Before(deadline), "queries must inherit a shorter deadline")
				<-queryCtx.Done()
				return false, queryCtx.Err()
			}
			return false, nil
		})
		require.NoError(t, ns.cleanRuntimeNode(deadlineCtx, sets.New[string]()))
		require.NoError(t, deadlineCtx.Err(), "saving must finish within the original GC deadline")
		require.Equal(t, 2, checks)
		require.Equal(t, 1, countDeleted(t, obj))
		require.Len(t, recorded.patches, 1)
		require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
		require.Equal(t, 3, countDeleted(t, obj), "remaining records must resume on the next GC")
	})
	t.Run("preserve concurrent additions removals and metadata", func(t *testing.T) {
		obj := create(t, "batch-concurrent", 1)
		obj.Status.Pods["removed"] = &networkv1beta1.RuntimePodStatus{PodID: "default/removed", Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{
			networkv1beta1.CNIStatusDeleted: {LastUpdateTime: metav1.Now()},
		}}
		require.NoError(t, c.Status().Update(ctx, obj))
		ns := service(obj, &runtimeGCClient{Client: c}, func(context.Context, string, string) (bool, error) {
			latest := &networkv1beta1.NodeRuntime{}
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), latest))
			delete(latest.Status.Pods, "removed")
			latest.Status.Pods["new"] = initial("new")
			require.NoError(t, c.Status().Update(ctx, latest))
			latest.Labels = map[string]string{"concurrent": "preserved"}
			require.NoError(t, c.Update(ctx, latest))
			return false, nil
		})
		require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
		require.Contains(t, obj.Status.Pods, "new")
		require.NotContains(t, obj.Status.Pods, "removed")
		require.Contains(t, obj.Status.Pods["pod-0"].Status, networkv1beta1.CNIStatusDeleted)
		require.Equal(t, "preserved", obj.Labels["concurrent"])
	})
	for _, change := range []string{"remove", "refresh", "recreate"} {
		t.Run("revalidate candidate after "+change, func(t *testing.T) {
			obj := create(t, "batch-"+change, 1)
			recorded := &runtimeGCClient{Client: c}
			ns := service(obj, recorded, func(context.Context, string, string) (bool, error) {
				latest := &networkv1beta1.NodeRuntime{}
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), latest))
				switch change {
				case "remove":
					delete(latest.Status.Pods, "pod-0")
				case "refresh":
					latest.Status.Pods["pod-0"].Status[networkv1beta1.CNIStatusInitial].LastUpdateTime = metav1.Now()
				case "recreate":
					require.NoError(t, c.Delete(ctx, latest))
					status := latest.Status.DeepCopy()
					latest = &networkv1beta1.NodeRuntime{ObjectMeta: metav1.ObjectMeta{Name: obj.Name}}
					require.NoError(t, c.Create(ctx, latest))
					require.NotEqual(t, obj.UID, latest.UID)
					latest.Status = *status
				}
				require.NoError(t, c.Status().Update(ctx, latest))
				return false, nil
			})
			require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
			require.Equal(t, 0, countDeleted(t, obj))
			require.Empty(t, recorded.patches)
			if change == "remove" {
				require.NotContains(t, obj.Status.Pods, "pod-0")
			}
		})
	}
	for _, change := range []string{"metadata", "refresh"} {
		t.Run("retry and revalidate conflict from "+change, func(t *testing.T) {
			obj := create(t, "conflict-"+change, 1)
			recorded := &runtimeGCClient{Client: c}
			recorded.beforePatch = func() {
				latest := &networkv1beta1.NodeRuntime{}
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), latest))
				if change == "metadata" {
					latest.Labels = map[string]string{"concurrent": "preserved"}
					require.NoError(t, c.Update(ctx, latest))
				} else {
					latest.Status.Pods["pod-0"].Status[networkv1beta1.CNIStatusInitial].LastUpdateTime = metav1.Now()
					require.NoError(t, c.Status().Update(ctx, latest))
				}
			}
			ns := service(obj, recorded, func(context.Context, string, string) (bool, error) { return false, nil })
			require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
			deleted := countDeleted(t, obj)
			if change == "metadata" {
				require.Equal(t, 1, deleted)
				require.Len(t, recorded.patches, 2)
				require.Equal(t, "preserved", obj.Labels["concurrent"])
			} else {
				require.Zero(t, deleted)
				require.Len(t, recorded.patches, 1)
			}
		})
	}
	t.Run("query failures do not imply deletion", func(t *testing.T) {
		obj := create(t, "query-error", 1)
		recorded := &runtimeGCClient{Client: c}
		ns := service(obj, recorded, func(context.Context, string, string) (bool, error) { return false, fmt.Errorf("pod query failed") })
		require.NoError(t, ns.cleanRuntimeNode(ctx, sets.New[string]()))
		require.Zero(t, countDeleted(t, obj))
		require.Empty(t, recorded.patches)
	})
}
