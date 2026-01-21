package feature

import (
	"testing"

	"github.com/stretchr/testify/assert"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
)

func TestFeatureGates(t *testing.T) {
	// Test that feature gates are defined
	assert.NotNil(t, defaultKubernetesFeatureGates)

	// Test AutoDataPathV2 feature gate
	assert.Contains(t, defaultKubernetesFeatureGates, AutoDataPathV2)
	spec := defaultKubernetesFeatureGates[AutoDataPathV2]
	assert.True(t, spec.Default)

	// Test EFLO feature gate
	assert.Contains(t, defaultKubernetesFeatureGates, EFLO)
	spec = defaultKubernetesFeatureGates[EFLO]
	assert.True(t, spec.Default)

	// Test KubeProxyReplacement feature gate
	assert.Contains(t, defaultKubernetesFeatureGates, KubeProxyReplacement)
	spec = defaultKubernetesFeatureGates[KubeProxyReplacement]
	assert.False(t, spec.Default)

	// Test WriteCNIConfFirst feature gate
	assert.Contains(t, defaultKubernetesFeatureGates, WriteCNIConfFirst)
	spec = defaultKubernetesFeatureGates[WriteCNIConfFirst]
	assert.False(t, spec.Default)
}

func TestFeatureGateValues(t *testing.T) {
	// Test that feature gates can be checked
	gate := utilfeature.DefaultMutableFeatureGate

	// Test AutoDataPathV2
	enabled := gate.Enabled(AutoDataPathV2)
	assert.True(t, enabled, "AutoDataPathV2 should be enabled by default")

	// Test EFLO
	enabled = gate.Enabled(EFLO)
	assert.True(t, enabled, "EFLO should be enabled by default")

	// Test KubeProxyReplacement
	enabled = gate.Enabled(KubeProxyReplacement)
	assert.False(t, enabled, "KubeProxyReplacement should be disabled by default")

	// Test WriteCNIConfFirst
	enabled = gate.Enabled(WriteCNIConfFirst)
	assert.False(t, enabled, "WriteCNIConfFirst should be disabled by default")
}

func TestFeatureGateString(t *testing.T) {
	// Test that feature gate constants are strings
	assert.Equal(t, "AutoDataPathV2", string(AutoDataPathV2))
	assert.Equal(t, "EFLO", string(EFLO))
	assert.Equal(t, "KubeProxyReplacement", string(KubeProxyReplacement))
	assert.Equal(t, "WriteCNIConfFirst", string(WriteCNIConfFirst))
}
