package daemon

import (
	"encoding/json"

	"github.com/AliyunContainerService/terway/types"
	jsonpatch "github.com/evanphx/json-patch"
)

// getDynamicConfig returns (config, label, error) specified in node
// ("", "", nil) for no dynamic config for this node
func getDynamicConfig(k8s Kubernetes) (string, string, error) {
	label := k8s.GetNodeDynamicConfigLabel()
	if label == "" {
		return "", "", nil
	}

	cfg, err := k8s.GetDynamicConfigWithName(label)

	return cfg, label, err
}

func mergeConfigAndUnmarshal(topCfg, baseCfg []byte) (*types.Configure, error) {
	if len(topCfg) == 0 { // no topCfg, unmarshal baseCfg and return
		config := &types.Configure{}
		err := json.Unmarshal(baseCfg, config)
		return config, err
	}

	// MergePatch in RFC7396
	jsonBytes, err := jsonpatch.MergePatch(baseCfg, topCfg)
	if err != nil {
		return nil, err
	}

	config := &types.Configure{}
	err = json.Unmarshal(jsonBytes, config)

	return config, err
}
