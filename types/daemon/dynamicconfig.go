package daemon

import (
	"context"
	"fmt"

	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	corev1 "k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	cfg *Config
)

func GetConfig() *Config {
	return cfg
}

func SetConfig(c *Config) {
	cfg = c
}

// ConfigFromConfigMap get eni-config form configmap if nodeName is not empty dynamic config is read
func ConfigFromConfigMap(ctx context.Context, client client.Client, nodeName string) (*Config, error) {
	baseConfig, err := eniConfigFromConfigMap(ctx, client, "kube-system", "eni-config")
	if err != nil {
		return nil, err
	}
	if baseConfig == "" {
		return nil, fmt.Errorf("terway configmap has no eni_conf filed")
	}
	topConfig := ""
	if nodeName != "" {
		cmName := nodeDynamicConfigName(ctx, client, nodeName)
		if cmName != "" {
			topConfig, err = eniConfigFromConfigMap(ctx, client, "kube-system", cmName)
			if err != nil {
				return nil, err
			}
		}
	}

	eniConf, err := MergeConfigAndUnmarshal([]byte(topConfig), []byte(baseConfig))
	if err != nil {
		return nil, fmt.Errorf("error parse terway configmap eni-config, %w", err)
	}

	mod := modifiers.New()
	err = mod.Struct(ctx, eniConf)
	if err != nil {
		return nil, err
	}

	err = validator.New().Struct(eniConf)
	if err != nil {
		return nil, err
	}

	sgs := eniConf.GetSecurityGroups()
	if len(sgs) > 5 {
		return nil, fmt.Errorf("security groups should not be more than 5, current %d", len(sgs))
	}

	return eniConf, nil
}

func eniConfigFromConfigMap(ctx context.Context, client client.Client, namespace, name string) (string, error) {
	cm := &corev1.ConfigMap{}
	err := client.Get(ctx, k8stypes.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, cm)
	if err != nil {
		return "", fmt.Errorf("error get configmap %s/%s, %w", namespace, name, err)
	}
	return cm.Data["eni_conf"], nil
}

func nodeDynamicConfigName(ctx context.Context, client client.Client, nodeName string) string {
	node := &corev1.Node{}
	err := client.Get(ctx, k8stypes.NamespacedName{
		Name: nodeName,
	}, node)
	if err != nil {
		return ""
	}
	return node.Labels["terway-config"]
}
