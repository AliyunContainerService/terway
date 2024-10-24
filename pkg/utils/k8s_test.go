package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
)

func TestFinalStatus(t *testing.T) {
	now := metav1.Now()
	later := metav1.Time{Time: now.Add(1 * time.Hour)}
	earlier := metav1.Time{Time: now.Add(-1 * time.Hour)}

	tests := []struct {
		name           string
		status         map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo
		expectedStatus v1beta1.CNIStatus
		expectedInfo   *v1beta1.CNIStatusInfo
		expectedOk     bool
	}{
		{
			name:           "Empty map",
			status:         map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{},
			expectedStatus: "", // should return zero value
			expectedInfo:   nil,
			expectedOk:     false,
		},
		{
			name: "Single Initial status",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusInitial,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
		{
			name: "Single Deleted status",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusDeleted: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
		{
			name: "Both status with Initial newer",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: later},
				v1beta1.CNIStatusDeleted: {LastUpdateTime: earlier},
			},
			expectedStatus: v1beta1.CNIStatusInitial,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: later},
			expectedOk:     true,
		},
		{
			name: "Both status with Deleted newer",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: {LastUpdateTime: earlier},
				v1beta1.CNIStatusDeleted: {LastUpdateTime: later},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: later},
			expectedOk:     true,
		},
		{
			name: "Nil status info",
			status: map[v1beta1.CNIStatus]*v1beta1.CNIStatusInfo{
				v1beta1.CNIStatusInitial: nil,
				v1beta1.CNIStatusDeleted: {LastUpdateTime: now},
			},
			expectedStatus: v1beta1.CNIStatusDeleted,
			expectedInfo:   &v1beta1.CNIStatusInfo{LastUpdateTime: now},
			expectedOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultStatus, resultInfo, resultOk := RuntimeFinalStatus(tt.status)
			assert.Equal(t, tt.expectedStatus, resultStatus)
			assert.Equal(t, tt.expectedInfo, resultInfo)
			assert.Equal(t, tt.expectedOk, resultOk)
		})
	}
}
