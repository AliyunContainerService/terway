package eni

import (
	"context"
	"sync"
	"time"

	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/types/daemon"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("start controller", func() {
	Context("start controller", func() {
		const (
			nodeName = "test-multiip"
		)

		It("New Controller", func() {
			ctrl := NewCRDV2(testEnv.Config, nodeName, "default")
			Expect(ctrl).NotTo(BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			wg := &sync.WaitGroup{}

			err := ctrl.Run(ctx, nil, wg)
			Expect(err).NotTo(HaveOccurred())

			//  create node cr
			node := &networkv1beta1.Node{}
			node.Name = nodeName
			node.Spec.ENISpec = &networkv1beta1.ENISpec{
				VSwitchOptions:      []string{"vsw-xx"},
				SecurityGroupIDs:    []string{"sg-x"},
				VSwitchSelectPolicy: networkv1beta1.VSwitchSelectionPolicyMost,
				EnableERDMA:         false,
			}
			node.Spec.NodeMetadata = networkv1beta1.NodeMetadata{
				RegionID:     "cn-hangzhou",
				InstanceID:   "i-foo",
				InstanceType: "ecs",
				ZoneID:       "cn-hangzhou-i",
			}

			err = k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			// test multiIP allocate
			var allocResp chan *AllocResp

			GinkgoT().Logf("before: allocResp")

			allocResp, _ = ctrl.multiIP(ctx, &daemon.CNI{
				PodName:      "foo",
				PodNamespace: "default",
				PodID:        "default/foo",
				PodUID:       "pod-uid",
				NetNSPath:    "",
			}, &LocalIPRequest{})

			node.Status.NetworkInterfaces = map[string]*networkv1beta1.Nic{
				"eni-foo": {
					ID:     "eni-foo",
					Status: "InUse",
					IPv4: map[string]*networkv1beta1.IP{
						"10.0.0.1": {
							Status:  "Valid",
							PodID:   "default/foo",
							Primary: false,
							PodUID:  "pod-uid",
							IP:      "10.0.0.1",
						},
					},
					IPv4CIDR: "10.0.0.0/24",
					IPv6: map[string]*networkv1beta1.IP{
						"fd00::1": {
							Status:  "Valid",
							PodID:   "default/foo",
							Primary: false,
							PodUID:  "pod-uid",
							IP:      "fd00::1",
						},
					},
					IPv6CIDR: "fd00::/64",
				},
			}

			GinkgoT().Logf("node status: %v", node.Status)

			err = k8sClient.Status().Update(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			GinkgoT().Logf("print update succeed")

			select {
			case r := <-allocResp:
				Expect(r.Err).NotTo(HaveOccurred())
				Expect(r.NetworkConfigs).To(HaveLen(1))
				Expect(r.NetworkConfigs[0].ToRPC()[0].BasicInfo.PodIP.IPv4).To(Equal("10.0.0.1"))
			case <-ctx.Done():
				Fail("timeout waiting for allocation response")
			}

			wg.Wait()
		})
	})
})
