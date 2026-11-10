package eni

import (
	"context"
	"testing"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestSaveRuntimeStatusConcurrentWrites(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../apis/crds"}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	t.Run("create missing runtime before optimistic status patch", func(t *testing.T) {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "runtime-new-node"}}
		require.NoError(t, c.Create(ctx, node))
		r := &CRDV2{client: c, scheme: scheme, nodeName: node.Name, deletedPods: map[string]*networkv1beta1.RuntimePodStatus{"deleted": {PodID: "ns/deleted"}}}
		require.NoError(t, r.syncNodeRuntime(ctx))
		created := &networkv1beta1.NodeRuntime{}
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: node.Name}, created))
		require.NotEmpty(t, created.UID)
		require.NotEmpty(t, created.ResourceVersion)
		require.Equal(t, node.Name, created.Labels["name"])
		require.Len(t, created.OwnerReferences, 1)
		require.Equal(t, node.UID, created.OwnerReferences[0].UID)
		require.Contains(t, created.Status.Pods["deleted"].Status, networkv1beta1.CNIStatusDeleted)
		require.Empty(t, r.deletedPods)
		// An outdated cache reports NotFound, but the Create races with an
		// existing object. Keep that object's status and server identity.
		r.client = &missingRuntimeOnceClient{Client: c}
		existing, err := r.getRuntimeNode(ctx)
		require.NoError(t, err)
		require.Equal(t, created.UID, existing.UID)
		require.Equal(t, created.Status, existing.Status)
		require.Equal(t, created.ResourceVersion, existing.ResourceVersion)
		// Recreate after deletion, including an empty status with no changes.
		require.NoError(t, c.Delete(ctx, created))
		recreated, err := r.getRuntimeNode(ctx)
		require.NoError(t, err)
		require.NotEqual(t, created.UID, recreated.UID)
		require.NoError(t, saveStatus(ctx, c, recreated.DeepCopy(), recreated))
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: node.Name}, created))
		require.Equal(t, recreated.UID, created.UID)
	})
	obj := &networkv1beta1.NodeRuntime{ObjectMeta: metav1.ObjectMeta{Name: "runtime-concurrent"}}
	require.NoError(t, c.Create(ctx, obj))
	initial := func(id string) *networkv1beta1.RuntimePodStatus {
		return &networkv1beta1.RuntimePodStatus{PodID: id, Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{networkv1beta1.CNIStatusInitial: {LastUpdateTime: metav1.Now()}}}
	}
	obj.Status.Pods = map[string]*networkv1beta1.RuntimePodStatus{"old": initial("ns/old"), "removed": initial("ns/removed")}
	require.NoError(t, c.Status().Update(ctx, obj))
	before := obj.DeepCopy()
	pending := obj.DeepCopy()
	pending.Status.Pods["old"].Status = map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{networkv1beta1.CNIStatusDeleted: {LastUpdateTime: metav1.Now()}}
	latest := obj.DeepCopy()
	delete(latest.Status.Pods, "removed")
	latest.Status.Pods["new"] = initial("ns/new")
	require.NoError(t, c.Status().Update(ctx, latest))
	latest.Labels = map[string]string{"concurrent": "preserved"}
	require.NoError(t, c.Update(ctx, latest))
	require.True(t, apierrors.IsConflict(saveStatus(ctx, c, before, pending)))
	got := &networkv1beta1.NodeRuntime{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), got))
	require.Equal(t, latest.Status, got.Status)
	require.Equal(t, latest.Labels, got.Labels)
	// Recompute from a fresh snapshot. Only the intended marker is changed.
	before = got.DeepCopy()
	got.Status.Pods["old"].Status = pending.Status.Pods["old"].Status
	require.NoError(t, saveStatus(ctx, c, before, got))
	require.Contains(t, got.Status.Pods, "new")
	require.NotContains(t, got.Status.Pods, "removed")
	rv := got.ResourceVersion
	require.NoError(t, saveStatus(ctx, c, got.DeepCopy(), got))
	require.Equal(t, rv, got.ResourceVersion)
	require.NoError(t, c.Delete(ctx, got))
	got.Status.Pods["old"].PodID = "ns/changed"
	require.True(t, apierrors.IsNotFound(saveStatus(ctx, c, before, got)))
}

// missingRuntimeOnceClient models one stale cache miss against the real API.
type missingRuntimeOnceClient struct {
	client.Client
	missed bool
}

func (c *missingRuntimeOnceClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*networkv1beta1.NodeRuntime); ok && !c.missed {
		c.missed = true
		return apierrors.NewNotFound(schema.GroupResource{Group: "network.alibabacloud.com", Resource: "noderuntimes"}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// Inject a real concurrent write between reading a runtime and patching it.
type runtimeConflictClient struct {
	client.Client
	beforePatch func()
}

type runtimeConflictStatusWriter struct {
	client.SubResourceWriter
	owner *runtimeConflictClient
}

func (c *runtimeConflictClient) Status() client.SubResourceWriter {
	return &runtimeConflictStatusWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

func (w *runtimeConflictStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if f := w.owner.beforePatch; f != nil {
		w.owner.beforePatch = nil
		f()
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func TestRuntimeDeletionQueueSurvivesConflict(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../apis/crds"}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, networkv1beta1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	obj := &networkv1beta1.NodeRuntime{ObjectMeta: metav1.ObjectMeta{Name: "deletion-conflict"}}
	require.NoError(t, c.Create(ctx, obj))
	concurrent := &runtimeConflictClient{Client: c}
	concurrent.beforePatch = func() {
		latest := &networkv1beta1.NodeRuntime{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), latest))
		latest.Status.Pods = map[string]*networkv1beta1.RuntimePodStatus{"unrelated": {
			PodID: "ns/unrelated", Status: map[networkv1beta1.CNIStatus]*networkv1beta1.CNIStatusInfo{networkv1beta1.CNIStatusInitial: {LastUpdateTime: metav1.Now()}},
		}}
		require.NoError(t, c.Status().Update(ctx, latest))
	}
	r := &CRDV2{client: concurrent, nodeName: obj.Name, deletedPods: map[string]*networkv1beta1.RuntimePodStatus{"deleted": {PodID: "ns/deleted"}}}
	require.True(t, apierrors.IsConflict(r.syncNodeRuntime(ctx)))
	require.Contains(t, r.deletedPods, "deleted", "conflicts must not discard CNI DEL work")
	require.NoError(t, r.syncNodeRuntime(ctx))
	require.Empty(t, r.deletedPods)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	require.Contains(t, obj.Status.Pods, "unrelated")
	require.Contains(t, obj.Status.Pods["deleted"].Status, networkv1beta1.CNIStatusDeleted)
}
