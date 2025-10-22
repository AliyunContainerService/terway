//go:build e2e

package eflo

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

var (
	openAPI *aliyunClient.APIFacade

	ak, sk, regionID string
	vSwitchIDs       string
	securityGroupIDs string
	credentialPath   string

	instanceID string
	zoneID     string
	vpcID      string
)

func init() {
	flag.StringVar(&ak, "ak", "", "ak")
	flag.StringVar(&sk, "sk", "", "sk")
	flag.StringVar(&vpcID, "vpc-id", "", "vpcID")
	flag.StringVar(&regionID, "region-id", "cn-wulanchabu", "regionID")
	flag.StringVar(&zoneID, "zone-id", "", "zoneID")
	flag.StringVar(&credentialPath, "credential-path", "./credential", "AlibabaCloud credential path")
	flag.StringVar(&vSwitchIDs, "vswitch-ids", "", "extra vSwitchIDs")
	flag.StringVar(&securityGroupIDs, "security-group-ids", "", "extra securityGroupIDs")
	flag.StringVar(&instanceID, "instance-id", "", "eflo instance id")
}

func TestAPIs(t *testing.T) {
	flag.Parse()

	prov := provider.NewChainProvider(
		provider.NewAccessKeyProvider(ak, sk),
		provider.NewEncryptedFileProvider(provider.EncryptedFileProviderOptions{
			FilePath:      credentialPath,
			RefreshPeriod: 30 * time.Minute,
		}),
		provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{}),
	)

	c, err := credential.InitializeClientMgr(regionID, prov)
	if err != nil {
		panic(err)
	}

	openAPI = aliyunClient.NewAPIFacade(c, nil)

	RegisterFailHandler(Fail)
	RunSpecs(t, "EFLO Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = Describe("Test eflo api", func() {
	Context("test eno", func() {
		It("eno api test ", func() {
			ctx := context.Background()
			ctx = aliyunClient.SetBackendAPI(ctx, aliyunClient.BackendAPIEFLO)

			eni, err := openAPI.CreateNetworkInterfaceV2(ctx, &aliyunClient.CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					VSwitchID:        strings.Split(vSwitchIDs, ",")[0],
					SecurityGroupIDs: []string{strings.Split(securityGroupIDs, ",")[0]},
					ZoneID:           zoneID,
					InstanceID:       instanceID,
					VPCID:            vpcID,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				By("delete eni")
				err = openAPI.DeleteNetworkInterfaceV2(ctx, eni.NetworkInterfaceID)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("wait eni status")
			err = wait.ExponentialBackoff(wait.Backoff{
				Steps:    15,
				Duration: 10 * time.Second,
				Factor:   0,
			}, func() (done bool, err error) {
				enis, err := openAPI.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{eni.NetworkInterfaceID},
				})
				if err != nil {
					return false, err
				}
				if len(enis) == 0 {
					return false, fmt.Errorf("eni not found")
				}
				if enis[0].Status != "Available" {
					return false, nil
				}
				GinkgoT().Logf("eni status: %s", enis[0].Status)

				return true, nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("detach eni")
			err = openAPI.GetECS().DetachNetworkInterface(ctx, eni.NetworkInterfaceID, instanceID, "")
			Expect(err).NotTo(HaveOccurred())

			By("wait eni detached")
			err = wait.ExponentialBackoff(wait.Backoff{
				Steps:    15,
				Duration: 10 * time.Second,
				Factor:   0,
			}, func() (done bool, err error) {
				enis, err := openAPI.DescribeNetworkInterfaceV2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{eni.NetworkInterfaceID},
				})
				if err != nil {
					return false, err
				}
				if len(enis) == 0 {
					return false, fmt.Errorf("eni not found")
				}
				if enis[0].Status != "Unattached" {
					return false, nil
				}

				return true, nil
			})

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("test ecsHighDensity", func() {
		It("ecs api test ", func() {
			ctx := context.Background()

			eni, err := openAPI.GetECS().CreateNetworkInterface(ctx, &aliyunClient.CreateNetworkInterfaceOptions{
				NetworkInterfaceOptions: &aliyunClient.NetworkInterfaceOptions{
					VSwitchID:        strings.Split(vSwitchIDs, ",")[0],
					SecurityGroupIDs: []string{strings.Split(securityGroupIDs, ",")[0]},
				},
			})

			Expect(err).NotTo(HaveOccurred())

			defer func() {
				By("delete eni")
				err = openAPI.GetECS().DeleteNetworkInterface(ctx, eni.NetworkInterfaceID)
				Expect(err).NotTo(HaveOccurred())
			}()

			By("attach to lingjun node")
			err = openAPI.GetECS().AttachNetworkInterface(ctx, &aliyunClient.AttachNetworkInterfaceOptions{
				NetworkInterfaceID:     &eni.NetworkInterfaceID,
				InstanceID:             &instanceID,
				TrunkNetworkInstanceID: ptr.To("DenseModeTrunkEniId"),
			})
			Expect(err).NotTo(HaveOccurred())

			By("wait eni status")
			err = wait.ExponentialBackoff(wait.Backoff{
				Steps:    15,
				Duration: 10 * time.Second,
				Factor:   0,
			}, func() (done bool, err error) {
				enis, err := openAPI.GetECS().DescribeNetworkInterface2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{eni.NetworkInterfaceID},
				})
				if err != nil {
					return false, err
				}
				if len(enis) == 0 {
					return false, fmt.Errorf("eni not found")
				}
				if enis[0].Status != "InUse" {
					return false, nil
				}
				if enis[0].DeviceIndex == 0 {
					return false, fmt.Errorf("device index is 0")
				}
				GinkgoT().Logf("eni status: %s", enis[0].Status)

				return true, nil
			})
			Expect(err).NotTo(HaveOccurred())

			By("detach eni") // DenseModeTrunkEniId
			err = openAPI.GetECS().DetachNetworkInterface(ctx, eni.NetworkInterfaceID, instanceID, "")
			Expect(err).NotTo(HaveOccurred())

			By("wait eni detached")
			err = wait.ExponentialBackoff(wait.Backoff{
				Steps:    15,
				Duration: 10 * time.Second,
				Factor:   0,
			}, func() (done bool, err error) {
				enis, err := openAPI.GetECS().DescribeNetworkInterface2(ctx, &aliyunClient.DescribeNetworkInterfaceOptions{
					NetworkInterfaceIDs: &[]string{eni.NetworkInterfaceID},
				})
				if err != nil {
					return false, err
				}
				if len(enis) == 0 {
					return false, fmt.Errorf("eni not found")
				}
				if enis[0].Status != "Available" {
					return false, nil
				}

				return true, nil
			})

			Expect(err).NotTo(HaveOccurred())
		})
	})
})
