//go:build e2e

package eflo

import (
	"context"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

var (
	openAPI *aliyunClient.OpenAPI

	ak, sk, regionID string
	vSwitchIDs       string
	securityGroupIDs string

	instanceID string
	zoneID     string
)

func init() {
	flag.StringVar(&ak, "ak", "", "ak")
	flag.StringVar(&sk, "sk", "", "sk")
	flag.StringVar(&regionID, "region-id", "", "regionID")
	flag.StringVar(&zoneID, "zone-id", "", "zoneID")
	flag.StringVar(&vSwitchIDs, "vswitch-ids", "", "extra vSwitchIDs")
	flag.StringVar(&securityGroupIDs, "security-group-ids", "", "extra securityGroupIDs")
	flag.StringVar(&instanceID, "instance-id", "", "eflo instance id")

	var providers []credential.Interface
	providers = append(providers, credential.NewAKPairProvider(ak, sk))
	clientSet, err := credential.NewClientMgr(regionID, providers...)
	if err != nil {
		panic(err)
	}
	openAPI, err = aliyunClient.New(clientSet, aliyunClient.LimitConfig{})
	if err != nil {
		panic(err)
	}
}

func TestAPIs(t *testing.T) {
	flag.Parse()

	RegisterFailHandler(Fail)
	RunSpecs(t, "EFLO Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = Describe("Test eflo api", func() {
	Context("Test list eflo leni", func() {
		It("list leni", func() {
			ctx := logr.NewContext(context.Background(), zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

			enis, err := openAPI.DescribeLeniNetworkInterface(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
				InstanceID: &instanceID,
			})
			Expect(err).NotTo(HaveOccurred())

			for _, eni := range enis {
				Expect(eni.Type).To(Equal("Secondary"))
			}
		})
	})

	Context("Test Create LENI", func() {
		It("create leni and check status", func() {

			By("create leni")
			ctx := logr.NewContext(context.Background(), zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
			eni, err := openAPI.CreateElasticNetworkInterfaceV2(ctx, &aliyunClient.CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					VSwitchID:        strings.Split(vSwitchIDs, ",")[0],
					SecurityGroupIDs: []string{strings.Split(securityGroupIDs, ",")[0]},
					InstanceID:       instanceID,
					ZoneID:           zoneID,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(eni).NotTo(BeNil())
			Expect(eni.NetworkInterfaceID).NotTo(BeEmpty())

			By("check leni status")

			err = wait.ExponentialBackoff(wait.Backoff{
				Steps:    30,
				Duration: 10 * time.Second,
				Factor:   0,
			}, func() (done bool, err error) {
				enis, err := openAPI.DescribeLeniNetworkInterface(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
					InstanceID:          &instanceID,
					NetworkInterfaceIDs: &[]string{eni.NetworkInterfaceID},
				})
				if err != nil {
					return false, nil
				}
				if len(enis) == 0 {
					return false, nil
				}
				if enis[0].Status != "InUse" {
					return false, nil
				}

				return true, nil
			})

			Expect(err).NotTo(HaveOccurred())

			By("delete leni")

			err = openAPI.DeleteElasticNetworkInterface(ctx, eni.NetworkInterfaceID)
			Expect(err).NotTo(HaveOccurred())
		})

	})
})
