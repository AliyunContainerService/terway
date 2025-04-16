package metadata_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMetadata(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metadata Tests")
}

var _ = Describe("Metadata BDD Tests", func() {
	var server *httptest.Server

	testTokenExpire := false
	BeforeEach(func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/latest/meta-data/instance-id":
				if testTokenExpire {
					if r.Header.Get("X-aliyun-ecs-metadata-token") != "test-new-token" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				} else {
					if r.Header.Get("X-aliyun-ecs-metadata-token") != "test-token" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}

				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test-instance-id"))
			case "/latest/meta-data/instance/instance-type":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test-instance-type"))
			case "/latest/meta-data/region-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("cn-test"))
			case "/latest/meta-data/zone-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("cn-test-a"))
			case "/latest/meta-data/vswitch-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("vsw-test"))
			case "/latest/meta-data/vpc-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("vpc-test"))
			case "/latest/meta-data/vpc-cidr-block":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("172.16.0.0/12"))
			case "/latest/meta-data/mac":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("mac1"))
			case "/latest/meta-data/network/interfaces/macs/":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("mac1/\nmac2/"))
			case "/latest/meta-data/network/interfaces/macs/mac1/network-interface-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("eni-test"))
			case "/latest/meta-data/network/interfaces/macs/mac1/primary-ip-address":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.1.10"))
			case "/latest/meta-data/network/interfaces/macs/mac1/gateway":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.1.1"))
			case "/latest/meta-data/network/interfaces/macs/mac1/ipv6-gateway":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fd00:4004:400::1"))
			case "/latest/meta-data/network/interfaces/macs/mac1/private-ipv4s":
				ips := []string{"192.168.1.10", "192.168.1.11"}
				jsonBytes, _ := json.Marshal(ips)
				w.WriteHeader(http.StatusOK)
				w.Write(jsonBytes)
			case "/latest/meta-data/network/interfaces/macs/mac1/ipv6s":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("[fd00:4004:400::1,fd00:4004:400::2]"))
			case "/latest/meta-data/network/interfaces/macs/mac1/vswitch-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("vsw-test"))
			case "/latest/meta-data/network/interfaces/macs/mac1/vswitch-cidr-block":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.1.0/24"))
			case "/latest/meta-data/network/interfaces/macs/mac1/vswitch-ipv6-cidr-block":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fd00:4004:400::/56"))
			case "/latest/api/token":
				if testTokenExpire {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("test-new-token"))
				} else {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("test-token"))
				}

			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		metadata.MetadataBase = server.URL + "/latest/meta-data/"
		metadata.TokenURL = server.URL + "/latest/api/token"
	})

	AfterEach(func() {
		server.Close()
	})

	Context("When fetching instance ID", func() {
		It("should return the correct instance ID", func() {
			instanceID, err := metadata.GetLocalInstanceID()
			Expect(err).NotTo(HaveOccurred())
			Expect(instanceID).To(Equal("test-instance-id"))
		})
	})

	Context("When fetching instance Type", func() {
		It("should return the correct instance Type", func() {
			instanceType, err := metadata.GetInstanceType()
			Expect(err).NotTo(HaveOccurred())
			Expect(instanceType).To(Equal("test-instance-type"))
		})
	})

	Context("When fetching region ID", func() {
		It("should return the correct region ID", func() {
			regionID, err := metadata.GetLocalRegion()
			Expect(err).NotTo(HaveOccurred())
			Expect(regionID).To(Equal("cn-test"))
		})
	})

	Context("When fetching zone ID", func() {
		It("should return the correct zone ID", func() {
			zoneID, err := metadata.GetLocalZone()
			Expect(err).NotTo(HaveOccurred())
			Expect(zoneID).To(Equal("cn-test-a"))
		})
	})

	Context("When fetching vswitch ID", func() {
		It("should return the correct vswitch ID", func() {
			vswitchID, err := metadata.GetLocalVswitch()
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchID).To(Equal("vsw-test"))
		})
	})

	Context("When fetching vpc ID", func() {
		It("should return the correct vpc ID", func() {
			vpcID, err := metadata.GetLocalVPC()
			Expect(err).NotTo(HaveOccurred())
			Expect(vpcID).To(Equal("vpc-test"))
		})
	})

	Context("When fetching vpc CIDR", func() {
		It("should return the correct vpc CIDR", func() {
			vpcCIDR, err := metadata.GetLocalVPCCIDR()
			Expect(err).NotTo(HaveOccurred())
			Expect(vpcCIDR).To(Equal("172.16.0.0/12"))
		})
	})

	Context("When fetching ENI primary IP", func() {
		It("should return the correct primary IP", func() {
			ip, err := metadata.GetENIPrimaryIP("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.String()).To(Equal("192.168.1.10"))
		})
	})

	Context("When fetching ENI primary Addr", func() {
		It("should return the correct primary Addr", func() {
			ip, err := metadata.GetENIPrimaryAddr("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ip.String()).To(Equal("192.168.1.10"))
		})
	})

	Context("When fetching ENI ID", func() {
		It("should return the correct ENI ID", func() {
			eniID, err := metadata.GetENIID("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(eniID).To(Equal("eni-test"))
		})
	})

	Context("When fetching ENI Gateway", func() {
		It("should return the correct ENI Gateway", func() {
			gateway, err := metadata.GetENIGateway("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(gateway.String()).To(Equal("192.168.1.1"))
		})
	})

	Context("When fetching ENI Gateway Addr", func() {
		It("should return the correct ENI Gateway Addr", func() {
			gateway, err := metadata.GetENIGatewayAddr("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(gateway.String()).To(Equal("192.168.1.1"))
		})
	})

	Context("When fetching ENI IPv6 Gateway", func() {
		It("should return the correct ENI IPv6 Gateway", func() {
			gateway, err := metadata.GetENIV6Gateway("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(gateway.String()).To(Equal("fd00:4004:400::1"))
		})
	})

	Context("When fetching ENI IPv6 Gateway Addr", func() {
		It("should return the correct ENI IPv6 Gateway Addr", func() {
			gateway, err := metadata.GetENIV6GatewayAddr("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(gateway.String()).To(Equal("fd00:4004:400::1"))
		})
	})

	Context("When fetching ENI private IPs", func() {
		It("should return the list of private IPs", func() {
			ips, err := metadata.GetENIPrivateIPs("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ips)).To(Equal(2))
			Expect(ips[0].String()).To(Equal("192.168.1.10"))
			Expect(ips[1].String()).To(Equal("192.168.1.11"))
		})
	})

	Context("When fetching ENI IPv4s Addr", func() {
		It("should return the list of private IPv4s Addr", func() {
			ips, err := metadata.GetIPv4ByMac("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ips)).To(Equal(2))
			Expect(ips[0].String()).To(Equal("192.168.1.10"))
			Expect(ips[1].String()).To(Equal("192.168.1.11"))
		})
	})

	Context("When fetching ENI private IPv6 IPs", func() {
		It("should return the list of private IPv6 IPs", func() {
			ips, err := metadata.GetENIPrivateIPv6IPs("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ips)).To(Equal(2))
			Expect(ips[0].String()).To(Equal("fd00:4004:400::1"))
			Expect(ips[1].String()).To(Equal("fd00:4004:400::2"))
		})
	})

	Context("When fetching ENI IPv6s Addr", func() {
		It("should return the list of private IPv6s Addr", func() {
			ips, err := metadata.GetIPv6ByMac("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(ips)).To(Equal(2))
			Expect(ips[0].String()).To(Equal("fd00:4004:400::1"))
			Expect(ips[1].String()).To(Equal("fd00:4004:400::2"))
		})
	})

	Context("When fetching ENI VSwitch ID", func() {
		It("should return the correct ENI VSwitch ID", func() {
			vswitchID, err := metadata.GetENIVSwitchID("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchID).To(Equal("vsw-test"))
		})
	})

	Context("When fetching ENI VSwitch CIDR", func() {
		It("should return the correct ENI VSwitch CIDR", func() {
			vswitchCIDR, err := metadata.GetVSwitchCIDR("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchCIDR.String()).To(Equal("192.168.1.0/24"))
		})
	})

	Context("When fetching ENI VSwitch Prefix", func() {
		It("should return the correct ENI VSwitch Prefix", func() {
			vswitchPrefix, err := metadata.GetVSwitchPrefix("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchPrefix.String()).To(Equal("192.168.1.0/24"))
		})
	})

	Context("When fetching ENI VSwitch IPv6 CIDR", func() {
		It("should return the correct ENI VSwitch IPv6 CIDR", func() {
			vswitchCIDR, err := metadata.GetVSwitchIPv6CIDR("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchCIDR.String()).To(Equal("fd00:4004:400::/56"))
		})
	})

	Context("When fetching ENI VSwitch IPv6 Prefix", func() {
		It("should return the correct ENI VSwitch IPv6 Prefix", func() {
			vswitchPrefix, err := metadata.GetVSwitchIPv6Prefix("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(vswitchPrefix.String()).To(Equal("fd00:4004:400::/56"))
		})
	})

	Context("When fetching ENIs MAC", func() {
		It("should return the list of ENIs MAC", func() {
			enisMAC, err := metadata.GetENIsMAC()
			Expect(err).NotTo(HaveOccurred())
			Expect(len(enisMAC)).To(Equal(2))
			Expect(enisMAC[0]).To(Equal("mac1"))
			Expect(enisMAC[1]).To(Equal("mac2"))
		})
	})

	Context("When fetching Primary ENI MAC", func() {
		It("should return the correct Primary ENI MAC", func() {
			primaryENIMAC, err := metadata.GetPrimaryENIMAC()
			Expect(err).NotTo(HaveOccurred())
			Expect(primaryENIMAC).To(Equal("mac1"))
		})
	})

	Context("When fetching metadata with token retry", func() {
		It("should retry with a new token after receiving a 401", func() {
			metadata.MetadataBase = server.URL + "/latest/meta-data/"
			metadata.TokenURL = server.URL + "/latest/api/token"

			instanceID, err := metadata.GetLocalInstanceID()
			Expect(err).NotTo(HaveOccurred())
			Expect(instanceID).To(Equal("test-instance-id"))

			testTokenExpire = true
			instanceID, err = metadata.GetLocalInstanceID()
			Expect(err).NotTo(HaveOccurred())
			Expect(instanceID).To(Equal("test-instance-id"))
		})
	})

	Context("When fetching non-existent metadata", func() {
		It("should return an error for missing data", func() {
			_, err := metadata.GetENIPrimaryIP("nonexistent-mac")
			Expect(err).To(HaveOccurred())
			Expect(strings.Contains(err.Error(), "404")).To(BeTrue())
		})
	})
})
