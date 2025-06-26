package controlplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/types/route"
)

func TestParsePodNetworksFromAnnotation(t *testing.T) {
	tests := []struct {
		name           string
		podAnnotations map[string]string
		expectedResult *PodNetworksAnnotation
		expectedError  string
	}{
		{
			name:           "AnnotationDoesNotExist",
			podAnnotations: map[string]string{},
			expectedResult: &PodNetworksAnnotation{},
			expectedError:  "",
		},
		{
			name: "AnnotationExistsAndValid",
			podAnnotations: map[string]string{
				"k8s.aliyun.com/pod-networks": `{ "podNetworks": [{"vSwitchOptions": [
              "vsw-a","vsw-b","vsw-c"
            ], "interface": "eth0",
            "securityGroupIDs": [
                "sg-1"
            ]}]}`,
			},
			expectedResult: &PodNetworksAnnotation{
				PodNetworks: []PodNetworks{
					{
						Interface: "eth0",
						VSwitchOptions: []string{
							"vsw-a", "vsw-b", "vsw-c",
						},
						SecurityGroupIDs: []string{
							"sg-1",
						},
					},
				},
			},
			expectedError: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: test.podAnnotations,
				},
			}

			result, err := ParsePodNetworksFromAnnotation(pod)
			if test.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expectedResult, result)
			}
		})
	}
}

func TestParsePodNetworksFromRequest(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		expectedResult []PodNetworkRef
		expectedError  string
	}{
		{
			name:           "AnnotationDoesNotExist",
			annotations:    map[string]string{},
			expectedResult: nil,
			expectedError:  "",
		},
		{
			name: "ValidSingleNetwork",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[{"interfaceName": "eth0", "network": "network1", "defaultRoute": true}]`,
			},
			expectedResult: []PodNetworkRef{
				{
					InterfaceName: "eth0",
					Network:       "network1",
					DefaultRoute:  true,
				},
			},
			expectedError: "",
		},
		{
			name: "ValidMultipleNetworks",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[
					{"interfaceName": "eth0", "network": "network1", "defaultRoute": true},
					{"interfaceName": "eth1", "network": "network2", "defaultRoute": false}
				]`,
			},
			expectedResult: []PodNetworkRef{
				{
					InterfaceName: "eth0",
					Network:       "network1",
					DefaultRoute:  true,
				},
				{
					InterfaceName: "eth1",
					Network:       "network2",
					DefaultRoute:  false,
				},
			},
			expectedError: "",
		},
		{
			name: "NetworkWithRoutes",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[{
					"interfaceName": "eth0",
					"network": "network1",
					"defaultRoute": false,
					"routes": [
						{"dst": "10.0.0.0/8", "gw": "192.168.1.1"},
						{"dst": "172.16.0.0/12", "gw": "192.168.1.1"}
					]
				}]`,
			},
			expectedResult: []PodNetworkRef{
				{
					InterfaceName: "eth0",
					Network:       "network1",
					DefaultRoute:  false,
					Routes: []route.Route{
						{Dst: "10.0.0.0/8"},
						{Dst: "172.16.0.0/12"},
					},
				},
			},
			expectedError: "",
		},
		{
			name: "EmptyArray",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[]`,
			},
			expectedResult: []PodNetworkRef{},
			expectedError:  "",
		},
		{
			name: "InvalidJSON",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `{"invalid": json}`,
			},
			expectedResult: nil,
			expectedError:  "parse k8s.aliyun.com/pod-networks-request from pod annotataion",
		},
		{
			name: "EmptyJSON",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": ``,
			},
			expectedResult: nil,
			expectedError:  "parse k8s.aliyun.com/pod-networks-request from pod annotataion",
		},
		{
			name: "MalformedJSON",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[{"interfaceName": "eth0", "network": "network1"`,
			},
			expectedResult: nil,
			expectedError:  "parse k8s.aliyun.com/pod-networks-request from pod annotataion",
		},
		{
			name: "NetworkWithMinimalFields",
			annotations: map[string]string{
				"k8s.aliyun.com/pod-networks-request": `[{"interfaceName": "eth0", "network": "network1"}]`,
			},
			expectedResult: []PodNetworkRef{
				{
					InterfaceName: "eth0",
					Network:       "network1",
					DefaultRoute:  false, // omitempty fields should have zero values
				},
			},
			expectedError: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParsePodNetworksFromRequest(test.annotations)
			if test.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expectedResult, result)
			}
		})
	}
}
