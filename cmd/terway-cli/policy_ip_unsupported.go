//go:build !linux

package main

import "fmt"

// resolveExclusiveNodeIP is only meaningful on Linux, where the eniOnly SNAT
// rule is installed. Non-Linux builds keep the package
// compiling (terway-cli is cross-built) but the policy entry point is never run
// there, so it reports the capability as unavailable.
func resolveExclusiveNodeIP(_, _ bool) (string, string, error) {
	return "", "", fmt.Errorf("eni-only masquerade is only supported on linux")
}
