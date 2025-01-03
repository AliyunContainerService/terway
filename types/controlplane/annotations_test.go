package controlplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
