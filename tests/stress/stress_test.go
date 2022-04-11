//go:build e2e

package stress

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/tests/utils"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestStress(t *testing.T) {
	stress1 := features.New("stress1").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var err error
			count := int32(containerNetworkPods / 2)
			deploy := utils.DeploymentPause("stress1", cfg.Namespace(), count)
			err = cfg.Client().Resources().Create(ctx, deploy)
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, "COUNT", count)
			ctx = context.WithValue(ctx, "DEPLOYMENT", deploy)
			return ctx
		}).
		Assess("scale 20 times", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			count := ctx.Value("COUNT").(int32)
			deployment := ctx.Value("DEPLOYMENT").(*appsv1.Deployment)

			current := count
			var zero int32 = 0
			for i := 0; i < 20; i++ {
				err := wait.For(conditions.New(cfg.Client().Resources()).ResourceScaled(deployment, func(object k8s.Object) int32 {
					return object.(*appsv1.Deployment).Status.ReadyReplicas
				}, current), wait.WithTimeout(2*time.Minute))
				if err != nil {
					t.Error("failed waiting for deployment to be scaled up")
				}
				err = cfg.Client().Resources().Get(ctx, deployment.Name, deployment.Namespace, deployment)
				if err != nil {
					t.Error("failed get deployment")
				}
				if current > 0 {
					deployment.Spec.Replicas = &zero
					current = zero
				} else {
					deployment.Spec.Replicas = &count
					current = count
				}
				err = cfg.Client().Resources().Update(ctx, deployment)
				if err != nil {
					t.Error("failed scale deployment")
				}
			}
			return ctx
		}).Feature()
	stress2 := features.New("stress2").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var err error
			count := int32(containerNetworkPods - containerNetworkPods/2)
			deploy := utils.DeploymentPause("stress2", cfg.Namespace(), count)
			err = cfg.Client().Resources().Create(ctx, deploy)
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, "COUNT", count)
			ctx = context.WithValue(ctx, "DEPLOYMENT", deploy)
			return ctx
		}).
		Assess("scale 20 times", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			count := ctx.Value("COUNT").(int32)
			deployment := ctx.Value("DEPLOYMENT").(*appsv1.Deployment)

			current := count
			var zero int32 = 0
			for i := 0; i < 20; i++ {
				err := wait.For(conditions.New(cfg.Client().Resources()).ResourceScaled(deployment, func(object k8s.Object) int32 {
					return object.(*appsv1.Deployment).Status.ReadyReplicas
				}, current), wait.WithTimeout(2*time.Minute))
				if err != nil {
					t.Error("failed waiting for deployment to be scaled up")
				}
				err = cfg.Client().Resources().Get(ctx, deployment.Name, deployment.Namespace, deployment)
				if err != nil {
					t.Error("failed get deployment")
				}
				if current > 0 {
					deployment.Spec.Replicas = &zero
					current = zero
				} else {
					deployment.Spec.Replicas = &count
					current = count
				}
				err = cfg.Client().Resources().Update(ctx, deployment)
				if err != nil {
					t.Error("failed scale deployment")
				}
			}
			return ctx
		}).Feature()
	testenv.TestInParallel(t, stress1, stress2)
}
