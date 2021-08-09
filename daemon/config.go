package daemon

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
