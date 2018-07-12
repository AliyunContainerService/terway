package version

import "github.com/containernetworking/cni/pkg/version"

// specVersionSupported is the version of the CNI spec that's supported by the
// ENI plugin. It's set to 0.3.0, which means that we support the following
// commands:
// * ADD
// * DELETE
// * VERSION
// Refer to https://github.com/containernetworking/cni/blob/master/SPEC.md
// for details
var specVersionSupported = version.PluginSupports(  "0.3.0")

// GetSpecVersionSupported gets the version of the CNI spec that's supported
// by the ENI plugin
func GetSpecVersionSupported() version.PluginInfo {
	return specVersionSupported
}
