//go:build e2e

package erdma

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestERDMAAllocation(t *testing.T) {
	if skipTest {
		t.Skipf("cluster not support erdma")
	}
	erdma := features.New("ERDMA").WithLabel("env", "erdma").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(namespace, podName, map[string]string{"erdma": "true"}, nil)
			err := config.Client().Resources().Create(ctx, p)
			if err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Assess("pod running", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace},
			}
			err := wait.For(conditions.New(config.Client().Resources()).ResourceMatch(&pod, func(object k8s.Object) bool {
				p := object.(*corev1.Pod)
				if p.Status.Phase != corev1.PodRunning {
					return false
				}

				return true
			}), wait.WithTimeout(time.Second*50))
			if err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("erdma device", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			pod := corev1.Pod{}
			err := config.Client().Resources().Get(ctx, podName, namespace, &pod)
			if err != nil {
				t.Fatal(err)
			}
			errors := []error{}
			err = wait.For(func(ctx context.Context) (done bool, err error) {
				var stdout, stderr bytes.Buffer
				cmd := []string{"rdma", "dev"}
				err = config.Client().Resources().ExecInPod(ctx, namespace, podName, "foo", cmd, &stdout, &stderr)
				if err != nil {
					errors = append(errors, fmt.Errorf("failed %s %w", cmd, err))
					return false, nil
				}
				if strings.Contains(stdout.String(), "erdma") {
					return true, nil
				}
				return false, nil
			},
				wait.WithTimeout(2*time.Minute),
				wait.WithInterval(5*time.Second))
			if err != nil {
				t.Fatalf("error get erdma device: %v,%v", err, errors)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			p := newPod(namespace, podName, map[string]string{"erdma": "true"}, nil)
			_ = config.Client().Resources().Delete(ctx, p)
			return ctx
		}).
		Feature()

	testenv.Test(t, erdma)
}

func newPod(namespace, name string, label, anno map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: label, Annotations: anno},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "foo",
					Image:           "registry.cn-hangzhou.aliyuncs.com/k8s-conformance/nettest:v0.2.0",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"sleep", "infinity"},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							"aliyun/erdma": resource.MustParse("1"),
						},
						Requests: corev1.ResourceList{
							"aliyun/erdma": resource.MustParse("1"),
						},
					},
				},
			},
			TerminationGracePeriodSeconds: func(a int64) *int64 { return &a }(0),
		},
	}
}
