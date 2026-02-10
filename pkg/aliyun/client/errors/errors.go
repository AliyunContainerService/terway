package errors

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/alibabacloud-go/tea/tea"
	apiErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
)

const (
	ErrInternalError = "InternalError"

	ErrForbidden = "Forbidden.RAM"

	// InvalidVSwitchIDIPNotEnough AssignPrivateIpAddresses const error message
	// Reference: https://help.aliyun.com/document_detail/85917.html
	InvalidVSwitchIDIPNotEnough = "InvalidVSwitchId.IpNotEnough"

	QuotaExceededPrivateIPAddress = "QuotaExceeded.PrivateIpAddress"

	// ErrEniPerInstanceLimitExceeded CreateNetworkInterfaces const error message
	// Reference: https://help.aliyun.com/document_detail/85917.html
	ErrEniPerInstanceLimitExceeded = "EniPerInstanceLimitExceeded"

	// ErrSecurityGroupInstanceLimitExceed CreateNetworkInterfaces const error message
	// Reference: https://help.aliyun.com/document_detail/85917.html
	ErrSecurityGroupInstanceLimitExceed = "SecurityGroupInstanceLimitExceed"

	// ErrInvalidIPIPUnassigned see https://help.aliyun.com/document_detail/85919.html
	// for API UnassignPrivateIpAddresses
	ErrInvalidIPIPUnassigned = "InvalidIp.IpUnassigned"

	// ErrInvalidENINotFound ..
	// for API UnassignPrivateIpAddresses DetachNetworkInterface
	ErrInvalidENINotFound = "InvalidEniId.NotFound"

	ErrInvalidEcsIDNotFound = "InvalidEcsId.NotFound"

	ErrIPv4CountExceeded = "InvalidOperation.Ipv4CountExceeded"
	ErrIPv6CountExceeded = "InvalidOperation.Ipv6CountExceeded"

	// ErrInvalidENIState ..
	// for API DeleteNetworkInterface
	ErrInvalidENIState = "InvalidOperation.InvalidEniState"

	// ErrInvalidAllocationIDNotFound InvalidAllocationId.NotFound EIP not found
	ErrInvalidAllocationIDNotFound = "InvalidAllocationId.NotFound"

	// ErrThrottling .
	ErrThrottling = "Throttling"

	ErrOperationConflict = "Operation.Conflict"

	ErrIdempotentFailed = "IdempotentFailed"

	ErrEfloPrivateIPQuotaExecuted = 1013
	ErrEfloResourceNotFound       = 1011
)

// define well known err
var (
	ErrNotFound = errors.New("not found")
)

// ErrAssert check err is match errCode
// DEPRECATED
func ErrAssert(errCode string, err error) bool {
	var respErr apiErr.Error
	ok := errors.As(err, &respErr)
	if ok {
		return respErr.ErrorCode() == errCode
	}
	return false
}

func ErrorCodeIs(err error, codes ...string) bool {
	var respErr apiErr.Error
	ok := errors.As(err, &respErr)
	if !ok {
		return false
	}

	for _, code := range codes {
		if respErr.ErrorCode() == code {
			return true
		}
	}
	return false
}

// ErrorCodeIsAny checks if error matches any of the provided error codes.
// It supports both apiErr.Error and tea.SDKError types.
func ErrorCodeIsAny(err error, codes ...string) bool {
	// Try apiErr.Error first
	var respErr apiErr.Error
	ok := errors.As(err, &respErr)
	if ok {
		for _, code := range codes {
			if respErr.ErrorCode() == code {
				return true
			}
		}
	}

	// Try tea.SDKError
	var sdkErr *tea.SDKError
	ok = errors.As(err, &sdkErr)
	if ok {
		for _, code := range codes {
			if tea.StringValue(sdkErr.Code) == code {
				return true
			}
		}
	}

	return false
}

// ErrRequestID try to get requestID
func ErrRequestID(err error) string {
	var respErr *apiErr.ServerError
	ok := errors.As(err, &respErr)
	if ok {
		return respErr.RequestId()
	}
	return ""
}

type E struct {
	e apiErr.Error
}

func (e *E) Error() string {
	if e.e == nil {
		return ""
	}

	return fmt.Sprintf("errCode: %s, msg: %s, requestID: %s", e.e.ErrorCode(), e.e.Message(), ErrRequestID(e.e))
}

func (e *E) Unwrap() error {
	return e.e
}

func WarpError(err error) error {
	if err == nil {
		return nil
	}
	var respErr apiErr.Error
	ok := errors.As(err, &respErr)
	if !ok {
		return err
	}

	return &E{e: respErr}
}

// IsURLError if there is conn problem
func IsURLError(err error) bool {
	if err == nil {
		return false
	}

	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func WarpFn(codes ...string) CheckErr {
	return func(err error) bool {
		return ErrorCodeIs(err, codes...)
	}
}

type CheckErr = func(err error) bool

func ErrorIs(err error, fns ...CheckErr) bool {
	for _, fn := range fns {
		if fn(err) {
			return true
		}
	}
	return false
}

type EFLOCode struct {
	Code      int
	Message   string
	RequestID string
	Content   any
}

func (e *EFLOCode) Error() string {
	return fmt.Sprintf("errCode: %d, msg: %s, requestID: %s", e.Code, e.Message, e.RequestID)
}

func IsEfloCode(err error, code int) bool {
	var efloErr *EFLOCode
	if errors.As(err, &efloErr) {
		return efloErr.Code == code
	}
	return false
}

type E2 struct {
	e     *tea.SDKError
	extra []string
}

func (e *E2) Error() string {
	if e.e == nil {
		return ""
	}

	return fmt.Sprintf("errCode: %s, msg: %s, %s", tea.StringValue(e.e.Code), tea.StringValue(e.e.Message), strings.Join(e.extra, ","))
}

func (e *E2) Unwrap() error {
	return e.e
}

func WarpError2(err error, extra ...string) error {
	if err == nil {
		return nil
	}
	var respErr *tea.SDKError
	ok := errors.As(err, &respErr)
	if !ok {
		return err
	}

	return &E2{e: respErr, extra: extra}
}
