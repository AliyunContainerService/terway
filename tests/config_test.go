//go:build e2e

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/Jeffail/gabs/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestIPPool(t *testing.T) {
	feature := features.New("IPPool").
		Setup(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			if eniConfig.IPAMType != "crd" {
				t.Skipf("skip ipam type not crd")
			}

			// Check if all nodes are exclusive ENI or Lingjun nodes, skip pool test in this case
			nodeInfo, err := DiscoverNodeTypes(ctx, config.Client())
			if err != nil {
				t.Fatalf("failed to discover node types: %v", err)
			}
			if len(nodeInfo.ECSSharedENINodes) == 0 && len(nodeInfo.LingjunSharedENINodes) == 0 {
				t.Skipf("TestIPPool requires shared ENI nodes, all nodes are exclusive ENI or Lingjun nodes")
			}

			// Check terway daemonset name is terway-eniip
			if GetCachedTerwayDaemonSetName() != "terway-eniip" {
				t.Skipf("TestIPPool requires terway-eniip daemonset, current: %s", GetCachedTerwayDaemonSetName())
			}

			// Check terway version >= v1.16.1
			if !RequireTerwayVersion("v1.16.1") {
				t.Skipf("TestIPPool requires terway version >= v1.16.1, current version: %s", GetCachedTerwayVersion())
			}

			return ctx
		}).
		Assess("step 1: test release all idles", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Step 1: Modified eni_config with ip_pool_sync_period=30s, max_pool_size=0, min_pool_size=0 and restarted Terwayd")
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni_config: %v", err)
			}
			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_config: %v", err)
			}
			_, err = eniJson.Set("30s", "ip_pool_sync_period")
			if err != nil {
				t.Fatalf("failed to set ip_pool_sync_period: %v", err)
			}
			_, err = eniJson.Set(0, "max_pool_size")
			if err != nil {
				t.Fatalf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(0, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			cm.Data["eni_conf"] = eniJson.String()
			err = config.Client().Resources().Update(ctx, cm)
			if err != nil {
				t.Fatalf("failed to update eni_config: %v", err)
			}

			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terwayd: %v", err)
			}

			t.Log("Step 2: Verified node CR config update and IP preheating completion")

			err = wait.For(func(ctx context.Context) (done bool, err error) {
				nodes := &networkv1beta1.NodeList{}
				err = config.Client().Resources().List(ctx, nodes)
				if err != nil {
					t.Fatalf("failed to get node cr: %v", err)
					return false, err
				}
				for _, node := range nodes.Items {
					if isExclusiveENINode(&node) {
						continue
					}
					idle := 0
					for _, eni := range node.Status.NetworkInterfaces {
						if eni.Status != aliyunClient.ENIStatusInUse {
							continue
						}
						for _, ip := range eni.IPv4 {
							if ip.Status != networkv1beta1.IPStatusValid {
								continue
							}
							if ip.Primary {
								continue
							}
							if ip.PodID == "" {
								idle++
							}
						}
					}
					if idle > 0 {
						t.Logf("node %v has %v idle ip", node.Name, idle)
						return false, nil
					}
				}
				return true, nil
			}, wait.WithTimeout(5*time.Minute), wait.WithInterval(5*time.Second))
			if err != nil {
				t.Fatalf("failed to wait for node cr config update: %v", err)
			}
			return ctx
		}).
		Assess("step 2: test preheating", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Step 1: Modified eni_config with ip_pool_sync_period=30s, max_pool_size=5, min_pool_size=5 and restarted Terwayd")
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni_config: %v", err)
			}
			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_config: %v", err)
			}
			_, err = eniJson.Set("30s", "ip_pool_sync_period")
			if err != nil {
				t.Fatalf("failed to set ip_pool_sync_period: %v", err)
			}
			_, err = eniJson.Set(5, "max_pool_size")
			if err != nil {
				t.Fatalf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(5, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			cm.Data["eni_conf"] = eniJson.String()
			err = config.Client().Resources().Update(ctx, cm)
			if err != nil {
				t.Fatalf("failed to update eni_config: %v", err)
			}

			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terwayd: %v", err)
			}

			t.Log("Step 2: verify idle ip added")

			err = wait.For(func(ctx context.Context) (done bool, err error) {
				nodes := &networkv1beta1.NodeList{}
				err = config.Client().Resources().List(ctx, nodes)
				if err != nil {
					t.Fatalf("failed to get node cr: %v", err)
					return false, err
				}
				for _, node := range nodes.Items {
					if isExclusiveENINode(&node) {
						continue
					}
					idle := 0
					for _, eni := range node.Status.NetworkInterfaces {
						if eni.Status != aliyunClient.ENIStatusInUse {
							continue
						}
						for _, ip := range eni.IPv4 {
							if ip.Status != networkv1beta1.IPStatusValid {
								continue
							}
							if ip.PodID == "" {
								idle++
							}
						}
					}
					if idle <= 3 {
						t.Logf("node %v has %v idle ip", node.Name, idle)
						return false, nil
					}
				}
				return true, nil
			}, wait.WithTimeout(5*time.Minute), wait.WithInterval(5*time.Second))
			if err != nil {
				t.Fatalf("failed to wait for node cr config update: %v", err)
			}
			return ctx
		}).
		Assess("step 3: test ip_pool_sync_period", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			t.Log("Step 1: Modified eni_config with ip_pool_sync_period=5m, max_pool_size=0, min_pool_size=0 and restarted Terwayd")
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni_config: %v", err)
			}
			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_config: %v", err)
			}
			_, err = eniJson.Set("5m", "ip_pool_sync_period")
			if err != nil {
				t.Fatalf("failed to set ip_pool_sync_period: %v", err)
			}
			_, err = eniJson.Set(0, "max_pool_size")
			if err != nil {
				t.Fatalf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(0, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			cm.Data["eni_conf"] = eniJson.String()
			err = config.Client().Resources().Update(ctx, cm)
			if err != nil {
				t.Fatalf("failed to update eni_config: %v", err)
			}

			err = restartTerway(ctx, config)
			if err != nil {
				t.Fatalf("failed to restart terwayd: %v", err)
			}

			t.Log("Step 2: verify ip should not be release in 4min")

			start := time.Now()
			err = wait.For(func(ctx context.Context) (done bool, err error) {
				nodes := &networkv1beta1.NodeList{}
				err = config.Client().Resources().List(ctx, nodes)
				if err != nil {
					t.Fatalf("failed to get node cr: %v", err)
				}
				for _, node := range nodes.Items {
					if isExclusiveENINode(&node) {
						continue
					}
					idle := 0
					for _, eni := range node.Status.NetworkInterfaces {
						if eni.Status != aliyunClient.ENIStatusInUse {
							continue
						}
						for _, ip := range eni.IPv4 {
							if ip.Status != networkv1beta1.IPStatusValid {
								continue
							}
							if ip.Primary {
								continue
							}
							if ip.PodID == "" {
								idle++
							}
						}
					}
					if idle > 0 {
						t.Logf("node %v has %v idle ip", node.Name, idle)
						_ = triggerNode(ctx, config, t, &node)

						return false, nil
					}
				}
				if time.Since(start) < 4*time.Minute {
					return false, fmt.Errorf("ip should not be release in 4min")
				}
				t.Logf("ip released after %v", time.Since(start))
				return true, nil
			}, wait.WithTimeout(10*time.Minute), wait.WithInterval(5*time.Second))
			if err != nil {
				t.Fatalf("failed to wait for node cr config update: %v", err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			cm := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", cm)
			if err != nil {
				t.Fatalf("failed to get eni_config: %v", err)
			}
			eniJson, err := gabs.ParseJSON([]byte(cm.Data["eni_conf"]))
			if err != nil {
				t.Fatalf("failed to parse eni_config: %v", err)
			}
			eniJson.Delete("ip_pool_sync_period")
			_, err = eniJson.Set(5, "max_pool_size")
			if err != nil {
				t.Fatalf("failed to set max_pool_size: %v", err)
			}
			_, err = eniJson.Set(0, "min_pool_size")
			if err != nil {
				t.Fatalf("failed to set min_pool_size: %v", err)
			}
			cm.Data["eni_conf"] = eniJson.String()
			err = config.Client().Resources().Update(ctx, cm)
			if err != nil {
				t.Fatalf("failed to update eni_config: %v", err)
			}
			return ctx
		}).
		Feature()

	testenv.Test(t, feature)
}
