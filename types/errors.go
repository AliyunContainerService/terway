package types

import (
	"fmt"
)

const (
	ErrInternalError      ErrCode = "InternalError"
	ErrInvalidArgsErrCode ErrCode = "InvalidArgs"
	ErrInvalidDataType    ErrCode = "InvalidDataType"

	ErrPodIsProcessing ErrCode = "PodIsProcessing"

	ErrResourceInvalid ErrCode = "ResourceInvalid"

	ErrOpenAPIErr     ErrCode = "OpenAPIErr"
	ErrPodENINotReady ErrCode = "PodENINotReady"
	ErrIPNotAllocated ErrCode = "IPNotAllocated"

	ErrIPOutOfSyncErr ErrCode = "OutIPOfSync"
)

type ErrCode string

type Error struct {
	Code ErrCode
	Msg  string

	R error
}

func (e *Error) Error() string {
	return fmt.Sprintf("code: %s, msg: %s", e.Code, e.Msg)
}

func (e *Error) Unwrap() error {
	return e.R
}
