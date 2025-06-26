package eni

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
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
			case "/latest/meta-data/network/interfaces/macs/mac2/network-interface-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("eni-test-mac2"))
			case "/latest/meta-data/network/interfaces/macs/mac2/primary-ip-address":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.2.10"))
			case "/latest/meta-data/network/interfaces/macs/mac2/gateway":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.2.1"))
			case "/latest/meta-data/network/interfaces/macs/mac2/ipv6-gateway":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fd00:4005:400::1"))
			case "/latest/meta-data/network/interfaces/macs/mac2/vswitch-id":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("vsw-test2"))
			case "/latest/meta-data/network/interfaces/macs/mac2/private-ipv4s":
				ips := []string{"192.168.2.10", "192.168.2.11"}
				jsonBytes, _ := json.Marshal(ips)
				w.WriteHeader(http.StatusOK)
				w.Write(jsonBytes)
			case "/latest/meta-data/network/interfaces/macs/mac2/ipv6s":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("[fd00:4005:400::1,fd00:4005:400::2]"))
			case "/latest/meta-data/network/interfaces/macs/mac2/vswitch-cidr-block":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("192.168.2.0/24"))
			case "/latest/meta-data/network/interfaces/macs/mac2/vswitch-ipv6-cidr-block":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fd00:4005:400::/56"))
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

	Context("Test meta", func() {
		It("should get mac address", func() {
			m := NewENIMetadata(true, true)
			v, err := m.GetENIByMac("mac1")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.MAC).To(Equal("mac1"))
		})

		It("should get multi enis", func() {
			m := NewENIMetadata(true, true)
			v, err := m.GetENIs(false)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(v)).To(Equal(1))
		})
	})
})
