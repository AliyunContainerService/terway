package feature

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
)

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultKubernetesFeatureGates))
}

const (
	// AutoDataPathV2 enable the new datapath feature.
	AutoDataPathV2 featuregate.Feature = "AutoDataPathV2"

	EFLO featuregate.Feature = "EFLO"
)

var defaultKubernetesFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	AutoDataPathV2: {Default: true, PreRelease: featuregate.Alpha},
	EFLO:           {Default: true, PreRelease: featuregate.Alpha},
}
