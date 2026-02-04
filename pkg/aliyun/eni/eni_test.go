package eni

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"

	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
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

		It("should get multi enis with containsMainENI true", func() {
			m := NewENIMetadata(true, true)
			v, err := m.GetENIs(true)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(v)).To(Equal(2))
		})
	})
})

func TestGetENIPrivateAddressesByMACv2(t *testing.T) {
	tests := []struct {
		name        string
		mac         string
		mockResult  []netip.Addr
		mockError   error
		expectError bool
	}{
		{
			name: "success - single IPv4",
			mac:  "00:11:22:33:44:55",
			mockResult: []netip.Addr{
				netip.MustParseAddr("192.168.1.10"),
			},
			mockError:   nil,
			expectError: false,
		},
		{
			name: "success - multiple IPv4",
			mac:  "00:11:22:33:44:66",
			mockResult: []netip.Addr{
				netip.MustParseAddr("192.168.1.10"),
				netip.MustParseAddr("192.168.1.11"),
				netip.MustParseAddr("192.168.1.12"),
			},
			mockError:   nil,
			expectError: false,
		},
		{
			name:        "error - metadata call failure",
			mac:         "00:11:22:33:44:77",
			mockResult:  nil,
			mockError:   errors.New("metadata service unavailable"),
			expectError: true,
		},
		{
			name:        "success - empty result",
			mac:         "00:11:22:33:44:88",
			mockResult:  []netip.Addr{},
			mockError:   nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC() // Force GC to help reset gomonkey state

			// Create patch for metadata.GetIPv4ByMac with proper closure
			patches := gomonkey.ApplyFunc(metadata.GetIPv4ByMac,
				func(mac string) ([]netip.Addr, error) {
					return tt.mockResult, tt.mockError
				})
			defer func() {
				patches.Reset()
				runtime.GC() // Force GC after reset
			}()

			eniMeta := NewENIMetadata(true, false)
			result, err := eniMeta.GetENIPrivateAddressesByMACv2(tt.mac)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.mockError, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tt.mockResult), len(result))
				for i, addr := range tt.mockResult {
					assert.Equal(t, addr, result[i])
				}
			}
		})
	}
}

func TestGetENIByMac_ErrorPaths(t *testing.T) {
	runtime.GC()
	patches := gomonkey.ApplyFunc(metadata.GetENIID, func(mac string) (string, error) {
		return "", errors.New("get eni id failed")
	})
	defer func() {
		patches.Reset()
		runtime.GC()
	}()

	m := NewENIMetadata(true, false)
	eni, err := m.GetENIByMac("mac1")
	assert.Error(t, err)
	assert.Nil(t, eni)
	assert.Contains(t, err.Error(), "get eni by id")
}

type mockInstanceMeta struct{}

func (m *mockInstanceMeta) GetRegionID() (string, error)  { return "", nil }
func (m *mockInstanceMeta) GetZoneID() (string, error)    { return "", nil }
func (m *mockInstanceMeta) GetVSwitchID() (string, error) { return "", nil }
func (m *mockInstanceMeta) GetPrimaryMAC() (string, error) {
	return "", errors.New("get primary mac failed")
}
func (m *mockInstanceMeta) GetInstanceID() (string, error)   { return "", nil }
func (m *mockInstanceMeta) GetInstanceType() (string, error) { return "", nil }

func TestGetENIs_GetPrimaryMACError(t *testing.T) {
	runtime.GC()
	patches := gomonkey.ApplyFunc(instance.GetInstanceMeta, func() instance.Interface {
		return &mockInstanceMeta{}
	})
	defer func() {
		patches.Reset()
		runtime.GC()
	}()

	m := NewENIMetadata(true, false)
	enis, err := m.GetENIs(false)
	assert.Error(t, err)
	assert.Nil(t, enis)
}

func TestGetENIs_GetENIsMACError(t *testing.T) {
	runtime.GC()
	patches := gomonkey.ApplyFunc(metadata.GetENIsMAC, func() ([]string, error) {
		return nil, errors.New("get macs failed")
	})
	defer func() {
		patches.Reset()
		runtime.GC()
	}()

	m := NewENIMetadata(true, false)
	enis, err := m.GetENIs(false)
	assert.Error(t, err)
	assert.Nil(t, enis)
}

func TestGetENIPrivateIPv6AddressesByMACv2(t *testing.T) {
	tests := []struct {
		name        string
		mac         string
		mockResult  []netip.Addr
		mockError   error
		expectError bool
	}{
		{
			name: "success - single IPv6",
			mac:  "00:11:22:33:44:55",
			mockResult: []netip.Addr{
				netip.MustParseAddr("fd00:4004:400::1"),
			},
			mockError:   nil,
			expectError: false,
		},
		{
			name: "success - multiple IPv6",
			mac:  "00:11:22:33:44:66",
			mockResult: []netip.Addr{
				netip.MustParseAddr("fd00:4004:400::1"),
				netip.MustParseAddr("fd00:4004:400::2"),
				netip.MustParseAddr("fd00:4004:400::3"),
			},
			mockError:   nil,
			expectError: false,
		},
		{
			name:        "error - metadata call failure",
			mac:         "00:11:22:33:44:77",
			mockResult:  nil,
			mockError:   errors.New("IPv6 not configured"),
			expectError: true,
		},
		{
			name:        "success - no IPv6 addresses",
			mac:         "00:11:22:33:44:88",
			mockResult:  nil,
			mockError:   nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC() // Force GC to help reset gomonkey state

			// Create patch for metadata.GetIPv6ByMac with proper closure
			patches := gomonkey.ApplyFunc(metadata.GetIPv6ByMac,
				func(mac string) ([]netip.Addr, error) {
					return tt.mockResult, tt.mockError
				})
			defer func() {
				patches.Reset()
				runtime.GC() // Force GC after reset
			}()

			eniMeta := NewENIMetadata(false, true)
			result, err := eniMeta.GetENIPrivateIPv6AddressesByMACv2(tt.mac)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.mockError, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				if tt.mockResult == nil {
					assert.Nil(t, result)
				} else {
					assert.Equal(t, len(tt.mockResult), len(result))
					for i, addr := range tt.mockResult {
						assert.Equal(t, addr, result[i])
					}
				}
			}
		})
	}
}
