package errors

import (
	"errors"
	"fmt"
	"net/url"

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
