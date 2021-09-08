package utils

import (
	"context"
	"fmt"

	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ParseClusterConfig() error {
	clusterID := viper.GetString("cluster-id")
	vpcID := viper.GetString("vpc-id")
	if clusterID == "" || vpcID == "" {
		clusterCM, err := K8sClient.CoreV1().ConfigMaps("kube-system").Get(context.TODO(), "ack-cluster-profile", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("neither clusterID or vpcID is set,%w", err)
		}
		if clusterID == "" {
			// managed k8s
			clusterID = clusterCM.Data["clusterid"]
		}
		if clusterID == "" {
			// dedicated k8s
			clusterID = clusterCM.Data["cluster-id"]
		}
		if vpcID == "" {
			vpcID = clusterCM.Data["vpcid"]
		}
	}
	if clusterID == "" || vpcID == "" {
		return fmt.Errorf("clutter-id or vpc-id is empty")
	}

	viper.Set("cluster-id", clusterID)
	viper.Set("vpc-id", vpcID)
	return nil
}

func Minimal(a int) int {
	if a < 0 {
		return 0
	}
	return a
}

func Min(a, b int) int {
	if a > b {
		return b
	}
	return a
}

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
