package eni

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"

	podENITypes "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestToRPC(t *testing.T) {
	t.Run("test with valid IPv4 and IPv6 allocations", func(t *testing.T) {
		l := &RemoteIPResource{
			podENI: podENITypes.PodENI{
				Spec: podENITypes.PodENISpec{
					Allocations: []podENITypes.Allocation{
						{
							IPv4:     "192.168.1.1",
							IPv4CIDR: "192.168.1.0/24",
							IPv6:     "fd00:db8::1",
							IPv6CIDR: "fd00:db8::/64",
							ENI: podENITypes.ENI{
								ID:  "eni-11",
								MAC: "00:00:00:00:00:00",
							},
							Interface:    "eth0",
							ExtraRoutes:  []podENITypes.Route{},
							DefaultRoute: true,
						},
					},
				},
				Status: podENITypes.PodENIStatus{
					Phase:       "",
					InstanceID:  "i-123456",
					TrunkENIID:  "eni-12345678",
					Msg:         "",
					PodLastSeen: metav1.Time{},
					ENIInfos: map[string]podENITypes.ENIInfo{
						"eni-11": {},
					},
				},
			},
			trunkENI: daemon.ENI{
				ID:  "eni-12345678",
				MAC: "00:00:00:00:00:00",
				GatewayIP: types.IPSet{
					IPv4: net.ParseIP("192.168.1.253"),
					IPv6: net.ParseIP("fd00:db8::fffe"),
				},
			},
		}

		// Call ToRPC and check the result
		result := l.ToRPC()
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result))
		assert.Equal(t, "192.168.1.1", result[0].BasicInfo.PodIP.IPv4)
		assert.Equal(t, "fd00:db8::1", result[0].BasicInfo.PodIP.IPv6)
		assert.Equal(t, "192.168.1.0/24", result[0].BasicInfo.PodCIDR.IPv4)
		assert.Equal(t, "fd00:db8::/64", result[0].BasicInfo.PodCIDR.IPv6)
		assert.Equal(t, "00:00:00:00:00:00", result[0].ENIInfo.MAC)
		assert.Equal(t, "eth0", result[0].IfName)
		assert.Equal(t, true, result[0].DefaultRoute)
	})
}
