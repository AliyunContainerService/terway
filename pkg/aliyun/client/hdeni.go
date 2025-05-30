package client

import (
	"context"
	"fmt"
)

func (a *EFLOService) CreateHDENI(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	return nil, fmt.Errorf("unsupported")
}

func (a *EFLOService) DeleteHDENI(ctx context.Context, eniID string) error {
	return fmt.Errorf("unsupported")
}

func (a *EFLOService) DescribeHDENI(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	return nil, fmt.Errorf("unsupported")
}

func (a *EFLOService) AttachHDENI(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	return fmt.Errorf("unsupported")
}

func (a *EFLOService) DetachHDENI(ctx context.Context, opts ...DetachNetworkInterfaceOption) error {
	return fmt.Errorf("unsupported")
}
