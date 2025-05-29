//go:build _default_build

package client

import (
	"context"
	"fmt"
)

func (a *OpenAPI) CreateHDENI(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	return nil, fmt.Errorf("not implemented")
}

func (a *OpenAPI) AttachHDENI(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	return fmt.Errorf("not implemented")
}

func (a *OpenAPI) DetachHDENI(ctx context.Context, opts ...DetachNetworkInterfaceOption) error {
	return fmt.Errorf("not implemented")
}

func (a *OpenAPI) DeleteHDENI(ctx context.Context, eniID string) error {
	return fmt.Errorf("not implemented")
}

func (a *OpenAPI) DescribeHDENI(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	return nil, fmt.Errorf("not implemented")
}
