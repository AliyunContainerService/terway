package daemon

import (
	"encoding/json"

	"github.com/AliyunContainerService/terway/types"
)

// getDynamicConfig returns config specified in node labels
// (nil, nil) for no dynamic config for this node
func getDynamicConfig(k8s Kubernetes) (string, error) {
	label := k8s.GetNodeDynamicConfigLabel()
	if label == "" {
		return "", nil
	}

	return k8s.GetDynamicConfigWithName(label)
}

func mergeConfigAndUnmarshal(topCfg, baseCfg []byte) (*types.Configure, error) {
	var top, base map[string]interface{}

	if len(topCfg) == 0 { // no topCfg, unmarshal baseCfg and return
		config := &types.Configure{}
		err := json.Unmarshal(baseCfg, config)
		return config, err
	}

	err := json.Unmarshal(topCfg, &top)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(baseCfg, &base)
	if err != nil {
		return nil, err
	}

	// merge
	for k, v := range top {
		base[k] = v
	}

	// marshal back to json
	jsonBytes, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}

	config := &types.Configure{}
	err = json.Unmarshal(jsonBytes, config)

	return config, err
}
