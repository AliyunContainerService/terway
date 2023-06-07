package errors

import (
	"errors"

	apiErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
)

const (
	// InvalidVSwitchIDIPNotEnough AssignPrivateIpAddresses const error message
	// Reference: https://help.aliyun.com/document_detail/85917.html
	InvalidVSwitchIDIPNotEnough = "InvalidVSwitchId.IpNotEnough"

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

	// ErrInvalidENIState ..
	// for API DeleteNetworkInterface
	ErrInvalidENIState = "InvalidOperation.InvalidEniState"

	// ErrInvalidAllocationIDNotFound InvalidAllocationId.NotFound EIP not found
	ErrInvalidAllocationIDNotFound = "InvalidAllocationId.NotFound"

	// ErrIncorrectEIPStatus ..
	// for API UnassociateEipAddress ReleaseEipAddress
	ErrIncorrectEIPStatus = "IncorrectEipStatus"

	// ErrAssociationDuplicated ..
	// for API AssociateEipAddress
	ErrAssociationDuplicated = "InvalidAssociation.Duplicated"

	// ErrIPNotInCbwp for eip
	ErrIPNotInCbwp = "OperationUnsupported.IpNotInCbwp"

	// ErrTaskConflict for eip
	ErrTaskConflict = "TaskConflict"

	// ErrThrottling .
	ErrThrottling = "Throttling"
)

// define well known err
var (
	ErrNotFound = errors.New("not found")
)

// ErrAssert check err is match errCode
func ErrAssert(errCode string, err error) bool {
	respErr, ok := err.(apiErr.Error)
	if ok {
		return respErr.ErrorCode() == errCode
	}
	return false
}

// ErrStatusCodeAssert check err is match errCode
func ErrStatusCodeAssert(code int, err error) bool {
	respErr, ok := err.(apiErr.Error)
	if ok {
		return respErr.HttpStatus() == code
	}
	return false
}

// ErrRequestID try to get requestID
func ErrRequestID(err error) string {
	respErr, ok := err.(*apiErr.ServerError)
	if ok {
		return respErr.RequestId()
	}
	return ""
}
