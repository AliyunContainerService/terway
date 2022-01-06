//go:build !windows
// +build !windows

package daemon

import "github.com/AliyunContainerService/terway/types"

func (f *eniIPFactory) setupENICompartment(eni *types.ENI) error {
	return nil
}

func (f *eniIPFactory) destroyENICompartment(eni *types.ENI) error {
	return nil
}
