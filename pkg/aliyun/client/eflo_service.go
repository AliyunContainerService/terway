package client

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/client/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/eflo"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"go.opentelemetry.io/otel/trace"
)

var _ EFLO = &EFLOService{}

const (
	APICreateElasticNetworkInterface = "CreateElasticNetworkInterface"
	APIAssignLeniPrivateIPAddress    = "AssignLeniPrivateIpAddress"
	APIAttachElasticNetworkInterface = "AttachElasticNetworkInterface"
	APIDetachElasticNetworkInterface = "DetachElasticNetworkInterface"
	APIDeleteElasticNetworkInterface = "DeleteElasticNetworkInterface"
	APIUnassignLeniPrivateIPAddress  = "UnassignLeniPrivateIpAddress"
	APIListLeniPrivateIPAddresses    = "ListLeniPrivateIpAddresses"
	APIListElasticNetworkInterfaces  = "ListElasticNetworkInterfaces"
	APIGetNodeInfoForPod             = "GetNodeInfoForPod"
)

// getDeleteCheckBackoff returns the backoff configuration for polling ENI deletion status.
// ~3 retries over ~30s: 4 checks with 10s intervals.
var getDeleteCheckBackoff = func() wait.Backoff {
	return wait.Backoff{
		Duration: 10 * time.Second,
		Factor:   1,
		Steps:    4,
	}
}

type EFLOService struct {
	ClientSet        credential.Client
	IdempotentKeyGen IdempotentKeyGen
	RateLimiter      *RateLimiter
	Tracer           trace.Tracer
}

func NewEFLOService(clientSet credential.Client, rateLimiter *RateLimiter, tracer trace.Tracer) *EFLOService {
	return &EFLOService{
		ClientSet:        clientSet,
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
	}
}

func (a *EFLOService) CreateElasticNetworkInterfaceV2(ctx context.Context, opts ...CreateNetworkInterfaceOption) (*NetworkInterface, error) {
	options := &CreateNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyCreateNetworkInterface(options)
	}

	req, rollBackFunc, err := options.EFLO(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && !apiErr.ErrorCodeIs(err, apiErr.ErrIdempotentFailed) {
			rollBackFunc()
		}
	}()

	err = a.RateLimiter.Wait(ctx, APICreateElasticNetworkInterface)
	if err != nil {
		return nil, err
	}

	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().CreateElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APICreateElasticNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed")
		return nil, err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("leni created", "leni", resp.Content.ElasticNetworkInterfaceId)

	return &NetworkInterface{
		NetworkInterfaceID:          resp.Content.ElasticNetworkInterfaceId,
		Type:                        ENITypeSecondary,
		NetworkInterfaceTrafficMode: ENITrafficModeStandard,
	}, err
}

func (a *EFLOService) DescribeLeniNetworkInterface(ctx context.Context, opts ...DescribeNetworkInterfaceOption) ([]*NetworkInterface, error) {
	options := &DescribeNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(options)
	}
	req := options.EFLO()

	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	resp, err := a.ClientSet.EFLO().ListElasticNetworkInterfaces(req)
	if err != nil {
		l.Error(err, "failed to list leni")
		return nil, err
	}
	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed to list leni")
		return nil, err
	}
	enis := make([]*NetworkInterface, 0)
	for _, data := range resp.Content.Data {

		l.WithValues(LogFieldRequestID, resp.RequestId).Info(APIListElasticNetworkInterfaces, "data", data)

		if data.Type != "CUSTOM" { // CUSTOM for our own card
			continue
		}

		var privateIPs []IPSet
		eni := &NetworkInterface{
			Status:                      data.Status,
			MacAddress:                  data.Mac,
			NetworkInterfaceID:          data.ElasticNetworkInterfaceId,
			VSwitchID:                   data.VSwitchId,
			VPCID:                       data.VpcId,
			PrivateIPAddress:            data.Ip,
			ZoneID:                      data.ZoneId,
			SecurityGroupIDs:            []string{data.SecurityGroupId},
			ResourceGroupID:             data.ResourceGroupId,
			IPv6Set:                     nil,
			Tags:                        nil,
			Type:                        ENITypeSecondary,
			InstanceID:                  data.NodeId,
			TrunkNetworkInterfaceID:     "",
			NetworkInterfaceTrafficMode: ENITrafficModeStandard,
			DeviceIndex:                 0,
		}

		if options.RawStatus == nil || !*options.RawStatus {
			// For now use ecs status
			switch eni.Status {
			case LENIStatusUnattached:
				eni.Status = ENIStatusAvailable
			case LENIStatusAvailable:
				eni.Status = ENIStatusInUse

			case LENIStatusCreateFailed, LENIStatusDeleting, LENIStatusDeleteFailed:
				eni.Status = ENIStatusDeleting
			}
		}

		privateIPs = append(privateIPs, IPSet{
			IPAddress: data.Ip,
			Primary:   true, // primary will not hav ipname
		})

		ipsData, err := a.ListLeniPrivateIPAddresses(ctx, eni.NetworkInterfaceID, "", "")
		if err != nil {
			return nil, err
		}

		for _, ips := range ipsData.Data {
			privateIPs = append(privateIPs, IPSet{
				IPAddress: ips.PrivateIpAddress,
				Primary:   false,
				IPName:    ips.IpName,
				IPStatus:  ips.Status,
			})
		}

		eni.PrivateIPSets = privateIPs
		enis = append(enis, eni)
	}
	return enis, nil
}

func (a *EFLOService) AssignLeniPrivateIPAddress2(ctx context.Context, opts ...AssignPrivateIPAddressOption) ([]IPSet, error) {
	ctx, span := a.Tracer.Start(ctx, APIAssignLeniPrivateIPAddress)
	defer span.End()

	option := &AssignPrivateIPAddressOptions{}
	for _, opt := range opts {
		opt.ApplyAssignPrivateIPAddress(option)
	}

	req, rollBackFunc, err := option.EFLO(a.IdempotentKeyGen)
	if err != nil {
		return nil, err
	}
	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	var (
		resp     *eflo.AssignLeniPrivateIpAddressResponse
		innerErr error
	)

	err = wait.ExponentialBackoffWithContext(ctx, *option.Backoff, func(ctx context.Context) (bool, error) {
		innerErr = a.RateLimiter.Wait(ctx, APIAssignLeniPrivateIPAddress)
		if innerErr != nil {
			return true, innerErr
		}
		start := time.Now()
		resp, innerErr = a.ClientSet.EFLO().AssignLeniPrivateIpAddress(req)
		metric.OpenAPILatency.WithLabelValues(APIAssignLeniPrivateIPAddress, fmt.Sprint(innerErr != nil)).Observe(metric.MsSince(start))

		if innerErr != nil {
			innerErr = apiErr.WarpError(innerErr)
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(innerErr)).Error(innerErr, "failed")
			if apiErr.ErrorIs(innerErr, apiErr.IsURLError, apiErr.WarpFn(apiErr.ErrThrottling, apiErr.ErrInternalError, apiErr.ErrOperationConflict)) {
				return false, nil
			}
			return true, innerErr
		}

		if resp.Code != 0 {
			err = &apiErr.EFLOCode{
				Code:      resp.Code,
				Message:   resp.Message,
				RequestID: resp.RequestId,
				Content:   resp.Content,
			}
			l.Error(err, "failed")
			return true, err
		}
		return true, nil
	})
	if err != nil {
		if !apiErr.ErrorCodeIs(err, apiErr.ErrIdempotentFailed) {
			rollBackFunc()
		}
		return nil, err
	}

	ipName := resp.Content.IpName
	eniID := resp.Content.ElasticNetworkInterfaceId

	re := make([]IPSet, 0)
	err = retry.OnError(wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   1,
		Jitter:   0,
		Steps:    3,
	}, func(err error) bool {
		return true
	}, func() error {
		content, err := a.ListLeniPrivateIPAddresses(ctx, eniID, ipName, "")
		if err != nil {
			return err
		}
		for _, data := range content.Data {
			if data.IpName != ipName {
				continue
			}
			if data.Status != LENIIPStatusAvailable {
				return fmt.Errorf("ip %s status %s", data.PrivateIpAddress, data.Status)
			}

			re = append(re, IPSet{
				Primary:   false,
				IPAddress: data.PrivateIpAddress,
				IPName:    data.IpName,
				IPStatus:  data.Status,
			})
		}
		return nil
	})
	if err != nil {
		re = append(re, IPSet{
			Primary:  false,
			IPName:   ipName,
			IPStatus: "",
		})
	}

	l.Info("assign leni private ip address", "re", re)

	return re, err
}

func (a *EFLOService) UnAssignLeniPrivateIPAddresses2(ctx context.Context, eniID string, ips []IPSet) error {
	for _, ip := range ips {
		if ip.IPName == "" {
			continue
		}
		err := a.UnassignLeniPrivateIPAddress(ctx, eniID, ip.IPName)
		if err != nil {
			return err
		}
	}
	return nil
}

// WaitForLeniNetworkInterface wait status of eni
func (a *EFLOService) WaitForLeniNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*NetworkInterface, error) {
	ctx, span := a.Tracer.Start(ctx, "WaitForNetworkInterface")
	defer span.End()

	var eniInfo *NetworkInterface
	if eniID == "" {
		return nil, fmt.Errorf("eniID not set")
	}
	err := wait.ExponentialBackoff(backoff,
		func() (done bool, err error) {
			eni, err := a.DescribeLeniNetworkInterface(ctx, &DescribeNetworkInterfaceOptions{
				NetworkInterfaceIDs: &[]string{eniID},
				Status:              &status,
			})
			if err != nil {
				return false, nil
			}
			if len(eni) == 0 && ignoreNotExist {
				return true, apiErr.ErrNotFound
			}
			if len(eni) == 1 {
				if string(status) != "" && status != eni[0].Status {
					return false, nil
				}

				eniInfo = eni[0]
				return true, nil
			}
			return false, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error wait for eni %v to status %s, %w", eniID, status, err)
	}
	return eniInfo, nil
}

func (a *EFLOService) AttachLeni(ctx context.Context, opts ...AttachNetworkInterfaceOption) error {
	ctx, span := a.Tracer.Start(ctx, APIAttachElasticNetworkInterface)
	defer span.End()

	option := &AttachNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(option)
	}

	req, err := option.EFLO()
	if err != nil {
		return err
	}
	l := LogFields(logr.FromContextOrDiscard(ctx), req)
	err = a.RateLimiter.Wait(ctx, APIAttachElasticNetworkInterface)
	if err != nil {
		return err
	}

	start := time.Now()
	resp, err := a.ClientSet.EFLOV2().AttachElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIAttachElasticNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return apiErr.WarpError2(err, APIAttachElasticNetworkInterface)
	}
	if resp.Body == nil {
		return fmt.Errorf("empty response body")
	}

	if resp.Body.Code != nil && *resp.Body.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      int(FromPtr(resp.Body.Code)),
			Message:   FromPtr(resp.Body.Message),
			RequestID: FromPtr(resp.Body.RequestId),
		}
		return err
	}

	l.WithValues(LogFieldRequestID, FromPtr(resp.Body.RequestId)).Info("attach eni")
	return nil
}

func (a *EFLOService) DetachLeni(ctx context.Context, opts ...DetachNetworkInterfaceOption) error {
	ctx, span := a.Tracer.Start(ctx, APIDetachElasticNetworkInterface)
	defer span.End()

	option := &DetachNetworkInterfaceOptions{}
	for _, opt := range opts {
		opt.ApplyTo(option)
	}

	req, err := option.EFLO()
	if err != nil {
		return err
	}
	l := LogFields(logr.FromContextOrDiscard(ctx), req)

	err = a.RateLimiter.Wait(ctx, APIDetachElasticNetworkInterface)
	if err != nil {
		return err
	}

	start := time.Now()
	// 1017 Detaching
	// 1011 detached or not found
	resp, err := a.ClientSet.EFLOV2().DetachElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIDetachElasticNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError2(err, APIDetachElasticNetworkInterface)
		if apiErr.ErrorCodeIsAny(err, strconv.Itoa(apiErr.ErrEfloResourceNotFound)) {
			l.WithValues("err", err.Error()).Info("return detached or not found")
			return nil
		}
		return err
	}

	if resp.Body == nil {
		return fmt.Errorf("empty response body")
	}

	if resp.Body.Code != nil && *resp.Body.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      int(FromPtr(resp.Body.Code)),
			Message:   FromPtr(resp.Body.Message),
			RequestID: FromPtr(resp.Body.RequestId),
		}
		return err
	}

	l.WithValues(LogFieldRequestID, FromPtr(resp.Body.RequestId)).Info("detach leni success")
	return nil
}

func (a *EFLOService) DeleteElasticNetworkInterface(ctx context.Context, eniID string) error {
	req := eflo.CreateDeleteElasticNetworkInterfaceRequest()
	req.ElasticNetworkInterfaceId = eniID
	l := LogFields(logf.FromContext(ctx), req)

	err := a.RateLimiter.Wait(ctx, APIDeleteElasticNetworkInterface)
	if err != nil {
		return err
	}

	start := time.Now()
	resp, err := a.ClientSet.EFLO().DeleteElasticNetworkInterface(req)
	metric.OpenAPILatency.WithLabelValues(APIDeleteElasticNetworkInterface, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIsAny(err, strconv.Itoa(apiErr.ErrEfloResourceNotFound)) {
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Info("return not found")
			return nil
		}
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		// 1011 IpName/leni not exist
		if !apiErr.IsEfloCode(err, apiErr.ErrEfloResourceNotFound) {
			l.Error(err, "failed")
			return err
		}
		// already deleted, no polling needed
		l.WithValues(LogFieldRequestID, resp.RequestId).Info("leni already not found")
		return nil
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("delete request accepted, polling for deletion")

	// Async deletion: poll to confirm the LENI is actually deleted
	rawStatus := true
	err = wait.ExponentialBackoffWithContext(ctx, getDeleteCheckBackoff(), func(ctx context.Context) (bool, error) {
		enis, descErr := a.DescribeLeniNetworkInterface(ctx, &DescribeNetworkInterfaceOptions{
			NetworkInterfaceIDs: &[]string{eniID},
			RawStatus:           &rawStatus,
		})
		if descErr != nil {
			l.Info("failed to query leni during deletion check, retrying", "err", descErr)
			return false, nil
		}
		if len(enis) == 0 {
			return true, nil
		}
		if enis[0].Status == LENIStatusDeleteFailed {
			return true, fmt.Errorf("leni %s entered %s status", eniID, LENIStatusDeleteFailed)
		}
		l.Info("leni still exists, waiting for deletion", "status", enis[0].Status)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for leni %s to be deleted: %w", eniID, err)
	}

	l.Info("leni deletion confirmed")
	return nil
}

func (a *EFLOService) UnassignLeniPrivateIPAddress(ctx context.Context, eniID, ipName string) error {
	err := a.RateLimiter.Wait(ctx, APIUnassignLeniPrivateIPAddress)
	if err != nil {
		return err
	}

	req := eflo.CreateUnassignLeniPrivateIpAddressRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().UnassignLeniPrivateIpAddress(req)
	metric.OpenAPILatency.WithLabelValues(APIUnassignLeniPrivateIPAddress, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		if apiErr.ErrorCodeIsAny(err, strconv.Itoa(apiErr.ErrEfloResourceNotFound)) {
			l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Info("return not found or already unassigned")
			return nil
		}
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")
		return err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		// 1011 IpName/leni not exist
		if !apiErr.IsEfloCode(err, apiErr.ErrEfloResourceNotFound) {
			l.Error(err, "failed")
			return err
		}
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return nil
}

func (a *EFLOService) ListLeniPrivateIPAddresses(ctx context.Context, eniID, ipName, ipAddress string) (*eflo.Content, error) {
	req := eflo.CreateListLeniPrivateIpAddressesRequest()
	req.ElasticNetworkInterfaceId = eniID
	req.IpName = ipName
	req.PrivateIpAddress = ipAddress

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().ListLeniPrivateIpAddresses(req)
	metric.OpenAPILatency.WithLabelValues(APIListLeniPrivateIPAddresses, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		err = apiErr.WarpError(err)
		l.WithValues(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err, "failed")

		return nil, err
	}

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed")
		return nil, err
	}

	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success")

	return &resp.Content, nil
}

func (a *EFLOService) GetNodeInfoForPod(ctx context.Context, nodeID string) (*eflo.Content, error) {
	req := eflo.CreateGetNodeInfoForPodRequest()
	req.NodeId = nodeID

	l := LogFields(logf.FromContext(ctx), req)

	start := time.Now()
	resp, err := a.ClientSet.EFLO().GetNodeInfoForPod(req)
	metric.OpenAPILatency.WithLabelValues(APIGetNodeInfoForPod, fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		return nil, err
	}
	l.WithValues(LogFieldRequestID, resp.RequestId).Info("success",
		"HdeniQuota",
		resp.Content.HdeniQuota,
		"LeniQuota",
		resp.Content.LeniQuota,
	)

	if resp.Code != 0 {
		err = &apiErr.EFLOCode{
			Code:      resp.Code,
			Message:   resp.Message,
			RequestID: resp.RequestId,
			Content:   resp.Content,
		}
		l.Error(err, "failed")
		return nil, err
	}

	return &resp.Content, nil
}
