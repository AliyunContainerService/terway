package aliyun

import "github.com/denverdino/aliyungo/common"

const (
	// InvalidVSwitchIDIPNotEnough AssignPrivateIpAddresses const error message
	// Reference: https://help.aliyun.com/document_detail/85917.html
	InvalidVSwitchIDIPNotEnough = "InvalidVSwitchId.IpNotEnough"

	// ErrInvalidIPIPUnassigned see https://help.aliyun.com/document_detail/85919.html
	ErrInvalidIPIPUnassigned = "InvalidIp.IpUnassigned"
)

// ErrAssert check err is match errCode
func ErrAssert(errCode string, err error) bool {
	respErr, ok := err.(*common.Error)
	if ok {
		return respErr.Code == errCode
	}
	return false
}
