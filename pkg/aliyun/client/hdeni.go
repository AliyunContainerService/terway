package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/wait"
)

func (a *OpenAPI) CreateHDENI(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	return nil, fmt.Errorf("unsupported")
}

func (a *OpenAPI) DeleteHDENI(ctx context.Context, eniID string) error {
	return fmt.Errorf("unsupported")
}

func (a *OpenAPI) DescribeHDENI(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	return nil, fmt.Errorf("unsupported")
}

func (a *OpenAPI) WaitForHDENI(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	return nil, fmt.Errorf("unsupported")
}
