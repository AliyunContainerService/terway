package client

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/util/wait"
)

var ErrNotImplemented = errors.New("not implemented")

func (a *OpenAPI) CreateNetworkInterfaceV2(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.CreateNetworkInterface(ctx, opts...)
	case BackendAPIEFLO:
		return a.CreateElasticNetworkInterfaceV2(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.CreateHDENI(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *OpenAPI) DescribeNetworkInterfaceV2(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.DescribeNetworkInterface2(ctx, opts...)
	case BackendAPIEFLO:
		return a.DescribeLeniNetworkInterface(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.DescribeHDENI(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *OpenAPI) AttachNetworkInterfaceV2(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.AttachNetworkInterface(ctx, opts...)
	case BackendAPIEFLO:
		return nil
	case BackendAPIEFLOHDENI:
		return nil
	}
	return ErrNotImplemented
}

func (a *OpenAPI) DetachNetworkInterfaceV2(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
	case BackendAPIEFLO:
		return nil
	case BackendAPIEFLOHDENI:
		return nil
	}
	return ErrNotImplemented
}

func (a *OpenAPI) DeleteNetworkInterfaceV2(ctx context.Context, eniID string) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.DeleteNetworkInterface(ctx, eniID)
	case BackendAPIEFLO:
		return a.DeleteElasticNetworkInterface(ctx, eniID)
	case BackendAPIEFLOHDENI:
		return a.DeleteHDENI(ctx, eniID)
	}
	return ErrNotImplemented
}

func (a *OpenAPI) AssignPrivateIPAddressV2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.AssignPrivateIPAddress2(ctx, opts...)
	case BackendAPIEFLO:
		return a.AssignLeniPrivateIPAddress2(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *OpenAPI) UnAssignPrivateIPAddressesV2(ctx context.Context, eniID string, ips []IPSet) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.UnAssignPrivateIPAddresses2(ctx, eniID, ips)
	case BackendAPIEFLO:
		return a.UnAssignLeniPrivateIPAddresses2(ctx, eniID, ips)
	}
	return ErrNotImplemented
}

func (a *OpenAPI) AssignIpv6AddressesV2(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]IPSet, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.AssignIpv6Addresses2(ctx, opts...)
	case BackendAPIEFLO:
		return nil, ErrNotImplemented
	}
	return nil, ErrNotImplemented
}

func (a *OpenAPI) UnAssignIpv6AddressesV2(ctx context.Context, eniID string, ips []IPSet) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.UnAssignIpv6Addresses2(ctx, eniID, ips)
	case BackendAPIEFLO:
		return ErrNotImplemented
	}
	return ErrNotImplemented
}

func (a *OpenAPI) WaitForNetworkInterfaceV2(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.WaitForNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
	case BackendAPIEFLO:
		return a.WaitForLeniNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
	case BackendAPIEFLOHDENI:
		return a.WaitForHDENI(ctx, eniID, status, backoff, ignoreNotExist)
	}
	return nil, ErrNotImplemented
}
