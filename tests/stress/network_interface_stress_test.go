//go:build e2e

package stress

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestNetworkInterfaceStress(t *testing.T) {
	const (
		totalCRs       = 150000
		updateQPS      = 1000
		updateDuration = 10 * time.Minute
	)

	f := features.New("NetworkInterfaceStress").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			restConfig := cfg.Client().RESTConfig()
			restConfig.QPS = 3000
			restConfig.Burst = 5000
			c, err := client.New(restConfig, client.Options{
				Scheme: cfg.Client().Resources().GetScheme(),
			})
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("Creating %d NetworkInterface CRs...", totalCRs)

			g, _ := errgroup.WithContext(ctx)
			g.SetLimit(100)

			start := time.Now()
			for i := 0; i < totalCRs; i++ {
				idx := i
				g.Go(func() error {
					name := fmt.Sprintf("stress-eni-%d", idx)
					trunk := rand.Intn(2) == 0
					ni := &v1beta1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1beta1.NetworkInterfaceSpec{
							ENI: v1beta1.ENI{
								ID:              fmt.Sprintf("eni-%d", idx),
								VPCID:           fmt.Sprintf("vpc-%d", rand.Intn(100)),
								MAC:             randomMAC(),
								Zone:            fmt.Sprintf("zone-%d", rand.Intn(10)),
								VSwitchID:       fmt.Sprintf("vsw-%d", rand.Intn(100)),
								ResourceGroupID: fmt.Sprintf("rg-%d", rand.Intn(100)),
								SecurityGroupIDs: []string{
									fmt.Sprintf("sg-%d", rand.Intn(100)),
									fmt.Sprintf("sg-%d", rand.Intn(100)),
								},
								AttachmentOptions: v1beta1.AttachmentOptions{
									Trunk: &trunk,
								},
							},
						},
					}
					if err := c.Create(ctx, ni); err != nil {
						// Ignore already exists
						if client.IgnoreAlreadyExists(err) != nil {
							return fmt.Errorf("failed to create ni %s: %v", name, err)
						}
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				t.Fatal(err)
			}
			t.Logf("Created %d CRs in %v", totalCRs, time.Since(start))

			return ctx
		}).
		Assess("List Performance (No Cache)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Create a non-cached client
			uncachedClient, err := client.New(cfg.Client().RESTConfig(), client.Options{
				Scheme: cfg.Client().Resources().GetScheme(),
			})
			if err != nil {
				t.Fatal(err)
			}

			var totalDuration time.Duration
			for i := 0; i < 5; i++ {
				start := time.Now()
				var list v1beta1.NetworkInterfaceList
				if err := uncachedClient.List(ctx, &list); err != nil {
					t.Fatalf("failed to list: %v", err)
				}
				duration := time.Since(start)
				t.Logf("List %d took %v, items: %d", i+1, duration, len(list.Items))
				totalDuration += duration
			}
			t.Logf("Average List Latency: %v", totalDuration/5)
			return ctx
		}).
		Assess("Update Performance", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			restConfig := cfg.Client().RESTConfig()
			restConfig.QPS = 3000
			restConfig.Burst = 5000
			c, err := client.New(restConfig, client.Options{
				Scheme: cfg.Client().Resources().GetScheme(),
			})
			if err != nil {
				t.Fatal(err)
			}

			limiter := rate.NewLimiter(rate.Limit(updateQPS), updateQPS)

			var (
				successCount int64
				failCount    int64
				totalLatency int64 // microseconds
			)

			ctx, cancel := context.WithTimeout(ctx, updateDuration)
			defer cancel()

			var wg sync.WaitGroup
			t.Logf("Starting update stress test for %v at %d QPS...", updateDuration, updateQPS)

			startTest := time.Now()

			// Use a pool of workers to execute updates
			workCh := make(chan int, 1000)
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for idx := range workCh {
						name := fmt.Sprintf("stress-eni-%d", idx)

						start := time.Now()

						patch := []byte(fmt.Sprintf(`{"status":{"phase":"%s","eniInfo":{"id":"%s"}}}`, "Bind", fmt.Sprintf("eni-%d", idx)))
						if rand.Intn(2) == 0 {
							patch = []byte(fmt.Sprintf(`{"status":{"phase":"%s","eniInfo":{"id":"%s"}}}`, "Unbind", fmt.Sprintf("eni-%d", idx)))
						}

						// Using MergePatch
						ni := &v1beta1.NetworkInterface{
							ObjectMeta: metav1.ObjectMeta{
								Name: name,
							},
						}
						err := c.Status().Patch(context.Background(), ni, client.RawPatch(types.MergePatchType, patch))

						latency := time.Since(start).Microseconds()
						atomic.AddInt64(&totalLatency, latency)

						if err != nil {
							atomic.AddInt64(&failCount, 1)
							if atomic.LoadInt64(&failCount) <= 10 {
								t.Logf("Update failed for %s: %v", name, err)
							}
						} else {
							atomic.AddInt64(&successCount, 1)
						}
					}
				}()
			}

			go func() {
				for {
					select {
					case <-ctx.Done():
						close(workCh)
						return
					default:
						if err := limiter.Wait(ctx); err != nil {
							// Context canceled
							close(workCh)
							return
						}
						// Pick random ID
						idx := rand.Intn(totalCRs)
						select {
						case workCh <- idx:
						case <-ctx.Done():
							close(workCh)
							return
						}
					}
				}
			}()

			wg.Wait()

			duration := time.Since(startTest)
			totalOps := successCount + failCount
			avgLatency := time.Duration(0)
			if totalOps > 0 {
				avgLatency = time.Duration(totalLatency/totalOps) * time.Microsecond
			}

			t.Logf("Update Stress Test Finished:")
			t.Logf("Duration: %v", duration)
			t.Logf("Total Ops: %d", totalOps)
			t.Logf("Success: %d", successCount)
			t.Logf("Failure: %d", failCount)
			t.Logf("Average Latency: %v", avgLatency)
			t.Logf("Actual QPS: %.2f", float64(totalOps)/duration.Seconds())

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Cleanup
			restConfig := cfg.Client().RESTConfig()
			restConfig.QPS = 3000
			restConfig.Burst = 5000
			c, err := client.New(restConfig, client.Options{
				Scheme: cfg.Client().Resources().GetScheme(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Cleaning up %d NetworkInterface CRs...", totalCRs)

			g, _ := errgroup.WithContext(ctx)
			g.SetLimit(100)

			for i := 0; i < totalCRs; i++ {
				idx := i
				g.Go(func() error {
					name := fmt.Sprintf("stress-eni-%d", idx)
					ni := &v1beta1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
					}
					_ = c.Delete(ctx, ni)
					return nil
				})
			}
			_ = g.Wait()
			t.Log("Cleanup finished")
			return ctx
		}).Feature()

	testenv.Test(t, f)
}

func randomMAC() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	buf[0] |= 2 // locally administered
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
}
