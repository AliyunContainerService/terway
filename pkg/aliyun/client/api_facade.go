package client

import (
	"context"
	"errors"

	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ ENI = &APIFacade{}

type APIFacade struct {
	ecsService  ECS
	efloService EFLO
	vpcService  VPC
}

func NewAPIFacade(clientSet credential.Client, limitConfig LimitConfig) *APIFacade {
	rateLimiter := NewRateLimiter(limitConfig)
	tracer := otel.Tracer("aliyun-api")

	return &APIFacade{
		ecsService:  NewECSService(clientSet, rateLimiter, tracer),
		efloService: NewEFLOService(clientSet, rateLimiter, tracer),
		vpcService:  NewVPCService(clientSet, rateLimiter, tracer),
	}
}

var ErrNotImplemented = errors.New("not implemented")

func (a *APIFacade) GetECS() ECS {
	return a.ecsService
}

func (a *APIFacade) GetVPC() VPC {
	return a.vpcService
}

func (a *APIFacade) GetEFLO() EFLO {
	return a.efloService
}

func (a *APIFacade) CreateNetworkInterfaceV2(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.CreateNetworkInterface(ctx, opts...)
	case BackendAPIEFLO:
		return a.efloService.CreateElasticNetworkInterfaceV2(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.efloService.CreateHDENI(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *APIFacade) DescribeNetworkInterfaceV2(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.DescribeNetworkInterface2(ctx, opts...)
	case BackendAPIEFLO:
		return a.efloService.DescribeLeniNetworkInterface(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.efloService.DescribeHDENI(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *APIFacade) AttachNetworkInterfaceV2(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.AttachNetworkInterface(ctx, opts...)
	case BackendAPIEFLO:
		return a.efloService.AttachLeni(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.efloService.AttachHDENI(ctx, opts...)
	}
	return ErrNotImplemented
}

func (a *APIFacade) DetachNetworkInterfaceV2(ctx context.Context, opts ...DetachNetworkInterfaceOption) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.DetachNetworkInterface2(ctx, opts...)
	case BackendAPIEFLO:
		return a.efloService.DetachLeni(ctx, opts...)
	case BackendAPIEFLOHDENI:
		return a.efloService.DetachHDENI(ctx, opts...)
	}
	return ErrNotImplemented
}

func (a *APIFacade) DeleteNetworkInterfaceV2(ctx context.Context, eniID string) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.DeleteNetworkInterface(ctx, eniID)
	case BackendAPIEFLO:
		return a.efloService.DeleteElasticNetworkInterface(ctx, eniID)
	case BackendAPIEFLOHDENI:
		return a.efloService.DeleteHDENI(ctx, eniID)
	}
	return ErrNotImplemented
}

func (a *APIFacade) AssignPrivateIPAddressV2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.AssignPrivateIPAddress2(ctx, opts...)
	case BackendAPIEFLO:
		return a.efloService.AssignLeniPrivateIPAddress2(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *APIFacade) UnAssignPrivateIPAddressesV2(ctx context.Context, eniID string, ips []IPSet) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.UnAssignPrivateIPAddresses2(ctx, eniID, ips)
	case BackendAPIEFLO:
		return a.efloService.UnAssignLeniPrivateIPAddresses2(ctx, eniID, ips)
	}
	return ErrNotImplemented
}

func (a *APIFacade) AssignIpv6AddressesV2(ctx context.Context, opts ...AssignIPv6AddressesOption) ([]IPSet, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.AssignIpv6Addresses2(ctx, opts...)
	}
	return nil, ErrNotImplemented
}

func (a *APIFacade) UnAssignIpv6AddressesV2(ctx context.Context, eniID string, ips []IPSet) error {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.UnAssignIpv6Addresses2(ctx, eniID, ips)
	}
	return ErrNotImplemented
}

func (a *APIFacade) WaitForNetworkInterfaceV2(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	switch GetBackendAPI(ctx) {
	case BackendAPIECS:
		return a.ecsService.WaitForNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
	case BackendAPIEFLO:
		return a.efloService.WaitForLeniNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
	}
	return nil, ErrNotImplemented
}
