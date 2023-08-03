//go:build test_env

package pod

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client/fake"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var tmpDir string

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx := context.Background()

	Describe("Set terway-controlplane config", func() {
		dir, err := os.MkdirTemp("", "terway-controlplane")
		Expect(err).ToNot(HaveOccurred())
		tmpDir = dir

		c := controlplane.Config{
			RegionID:                    "cn-hangzhou",
			VPCID:                       "fake",
			ClusterID:                   "fake",
			CustomStatefulWorkloadKinds: []string{"foo"},
		}
		cfgContents, _ := json.Marshal(c)
		cfgPath := filepath.Join(dir, "ctrl-config.yaml")
		err = os.WriteFile(cfgPath, cfgContents, 0640)
		err = flag.Set("config", cfgPath)
		Expect(err).ToNot(HaveOccurred())

		credentialPath := filepath.Join(dir, "ctrl-secret.yaml")
		err = os.WriteFile(credentialPath, []byte("{}"), 0640)
		err = flag.Set("credential", credentialPath)
		Expect(err).ToNot(HaveOccurred())

		flag.Parse()
	})

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "apis", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = networkv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	vsw, err := vswitch.NewSwitchPool(1000, "100m")
	Expect(err).ToNot(HaveOccurred())

	fakeClient := &fake.OpenAPI{
		VSwitches: map[string]vpc.VSwitch{
			"vsw-az-1-no-ip": {
				VSwitchId:               "vsw-az-1-no-ip",
				ZoneId:                  "az-1",
				AvailableIpAddressCount: 0,
			}, "vsw-az-1": {
				VSwitchId:               "vsw-az-1",
				ZoneId:                  "az-1",
				AvailableIpAddressCount: 100,
			}, "vsw-az-2": {
				VSwitchId:               "vsw-az-2",
				ZoneId:                  "az-2",
				AvailableIpAddressCount: 100,
			},
		},
	}
	err = register.Controllers[controllerName].Creator(k8sManager, &register.ControllerCtx{
		Config:         &controlplane.Config{},
		VSwitchPool:    vsw,
		AliyunClient:   fakeClient,
		DelegateClient: fakeClient,
	})
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	Describe("Create podNetworking", func() {
		Describe("Create elastic-ip podNetworking", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: "elastic-ip-podnetworking",
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					VSwitchOptions:   []string{"vsw-az-1"},
					SecurityGroupIDs: []string{"sg-1", "sg-2"},
				},
			}
			Expect(k8sClient.Create(ctx, created)).Should(Succeed())
			created.Status.Status = networkv1beta1.NetworkingStatusReady
			Expect(k8sClient.Update(ctx, created)).Should(Succeed())
		})
		Describe("Create fixed-ip podNetworking", func() {
			created := &networkv1beta1.PodNetworking{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fixed-ip-podnetworking",
				},
				Spec: networkv1beta1.PodNetworkingSpec{
					AllocationType:   networkv1beta1.AllocationType{Type: networkv1beta1.IPAllocTypeFixed},
					VSwitchOptions:   []string{"vsw-az-1"},
					SecurityGroupIDs: []string{"sg-1", "sg-2"},
				},
			}
			Expect(k8sClient.Create(ctx, created)).Should(Succeed())
			created.Status.Status = networkv1beta1.NetworkingStatusReady
			Expect(k8sClient.Update(ctx, created)).Should(Succeed())
		})
	})

	Describe("Create nodes", func() {
		Expect(k8sClient.Create(ctx, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-az-1",
				Labels: map[string]string{corev1.LabelTopologyZone: "az-1"},
			},
			Spec: corev1.NodeSpec{ProviderID: "n.i-az-1"},
		})).Should(Succeed())
	})

}, 5)

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
	if tmpDir != "" {
		err = os.RemoveAll(tmpDir)
		Expect(err).NotTo(HaveOccurred())
	}
})
