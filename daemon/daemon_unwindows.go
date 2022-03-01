//go:build !windows
// +build !windows

package daemon

func preStartResourceManager(daemonMode string, k8s Kubernetes) error {
	return nil
}
