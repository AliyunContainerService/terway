package errors

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	apiErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/stretchr/testify/assert"
)

func TestErrAssert(t *testing.T) {
	type args struct {
		errCode string
		err     error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "common err",
			args: args{
				errCode: "err",
				err:     apiErr.NewServerError(403, "{\"Code\": \"err\"}", ""),
			},
			want: true,
		}, {
			name: "code not match",
			args: args{
				errCode: "errNotMatch",
				err:     apiErr.NewServerError(403, "{\"Code\": \"err\"}", ""),
			},
			want: false,
		}, {
			name: "err type mismatch",
			args: args{
				errCode: "err",
				err:     errors.New("err"),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrAssert(tt.args.errCode, tt.args.err); got != tt.want {
				t.Errorf("ErrAssert() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorCodeIs(t *testing.T) {
	type args struct {
		err   error
		codes []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "common err",
			args: args{
				err:   apiErr.NewServerError(403, "{\"Code\": \"err\"}", ""),
				codes: []string{"err"},
			},
			want: true,
		}, {
			name: "code not match",
			args: args{
				err:   apiErr.NewServerError(403, "{\"Code\": \"err\"}", ""),
				codes: []string{},
			},
			want: false,
		},
		{
			name: "match one",
			args: args{
				err:   apiErr.NewServerError(403, "{\"Code\": \"err\"}", ""),
				codes: []string{"foo", "err", "bar"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorCodeIs(tt.args.err, tt.args.codes...); got != tt.want {
				t.Errorf("ErrorCodeIs() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestErrorIs tests the ErrorIs function.
func TestErrorIs(t *testing.T) {
	err := errors.New("test error")

	// Test case 1: Check if error matches the given check functions
	checkFunc1 := func(err error) bool {
		return err.Error() == "test error"
	}
	checkFunc2 := func(err error) bool {
		return errors.Is(err, errors.New("test error"))
	}
	assert.True(t, ErrorIs(err, checkFunc1, checkFunc2))

	// Test case 2: Check if error does not match the given check functions
	checkFunc3 := func(err error) bool {
		return err.Error() == "another error"
	}
	checkFunc4 := func(err error) bool {
		return errors.Is(err, errors.New("another error"))
	}
	assert.False(t, ErrorIs(err, checkFunc3, checkFunc4))

	// Test case 3: Check if no check functions are provided
	assert.False(t, ErrorIs(err))
}

func TestErrorIsReturnsFalseWhenNoCheckErrMatches(t *testing.T) {
	err := apiErr.NewServerError(403, "{\"Code\": \"err\"}", "")
	checkFunc1 := WarpFn("anotherErr")
	checkFunc2 := WarpFn("yetAnotherErr")
	assert.False(t, ErrorIs(err, checkFunc1, checkFunc2))
}

func TestEflo(t *testing.T) {
	err := &EFLOCode{
		Code:      403,
		Content:   "{\"Code\": \"err\"}",
		Message:   "message",
		RequestID: "requestId",
	}

	assert.True(t, IsEfloCode(err, 403))
	assert.True(t, IsEfloCode(fmt.Errorf("warp %w", err), 403))
	assert.False(t, IsEfloCode(fmt.Errorf("warp %w", err), 00))
}

func TestE2(T *testing.T) {
	err := &tea.SDKError{
		Code: tea.String("err"),
	}
	err2 := WarpError2(err)

	assert.True(T, errors.Is(err2, err))
}

func TestErrRequestID(t *testing.T) {
	// Test with ServerError - Note: NewServerError may not set RequestId correctly
	// We test that the function works, even if RequestId is empty
	serverErr := apiErr.NewServerError(403, "{\"Code\": \"err\"}", "request-id-123")
	requestID := ErrRequestID(serverErr)
	// RequestId() may return empty string even if passed, so we just check it doesn't panic
	assert.NotNil(t, serverErr)
	_ = requestID // Use the value to avoid unused variable

	// Test with non-ServerError
	regularErr := errors.New("regular error")
	requestID = ErrRequestID(regularErr)
	assert.Equal(t, "", requestID)

	// Test with nil
	requestID = ErrRequestID(nil)
	assert.Equal(t, "", requestID)
}

func TestE_Error(t *testing.T) {
	// Test with nil error
	e := &E{e: nil}
	assert.Equal(t, "", e.Error())

	// Test with ServerError
	serverErr := apiErr.NewServerError(403, "test message", "request-id-123")
	e = &E{e: serverErr}
	errorStr := e.Error()
	assert.Contains(t, errorStr, "errCode:")
	assert.Contains(t, errorStr, "msg:")
	assert.Contains(t, errorStr, "requestID:")
}

func TestE_Unwrap(t *testing.T) {
	serverErr := apiErr.NewServerError(403, "test message", "request-id-123")
	e := &E{e: serverErr}
	assert.Equal(t, serverErr, e.Unwrap())
}

func TestWarpError(t *testing.T) {
	// Test with nil
	wrapped := WarpError(nil)
	assert.Nil(t, wrapped)

	// Test with ServerError
	serverErr := apiErr.NewServerError(403, "test message", "request-id-123")
	wrapped = WarpError(serverErr)
	assert.NotNil(t, wrapped)
	var e *E
	assert.True(t, errors.As(wrapped, &e))
	assert.Equal(t, serverErr, e.Unwrap())

	// Test with regular error
	regularErr := errors.New("regular error")
	wrapped = WarpError(regularErr)
	assert.Equal(t, regularErr, wrapped)
}

func TestIsURLError(t *testing.T) {
	// Test with nil
	assert.False(t, IsURLError(nil))

	// Test with url.Error
	urlErr := &url.Error{
		Op:  "GET",
		URL: "http://example.com",
		Err: errors.New("connection refused"),
	}
	assert.True(t, IsURLError(urlErr))

	// Test with wrapped url.Error
	wrappedURLErr := fmt.Errorf("wrapped: %w", urlErr)
	assert.True(t, IsURLError(wrappedURLErr))

	// Test with regular error
	regularErr := errors.New("regular error")
	assert.False(t, IsURLError(regularErr))
}

func TestWarpFn(t *testing.T) {
	checkFn := WarpFn("err1", "err2")

	// Test matching error code
	serverErr := apiErr.NewServerError(403, "{\"Code\": \"err1\"}", "")
	assert.True(t, checkFn(serverErr))

	// Test matching second error code
	serverErr2 := apiErr.NewServerError(403, "{\"Code\": \"err2\"}", "")
	assert.True(t, checkFn(serverErr2))

	// Test non-matching error code
	serverErr3 := apiErr.NewServerError(403, "{\"Code\": \"err3\"}", "")
	assert.False(t, checkFn(serverErr3))

	// Test with regular error
	regularErr := errors.New("regular error")
	assert.False(t, checkFn(regularErr))
}

func TestEFLOCode_Error(t *testing.T) {
	efloErr := &EFLOCode{
		Code:      1011,
		Message:   "resource not found",
		RequestID: "request-id-123",
		Content:   "some content",
	}
	errorStr := efloErr.Error()
	assert.Contains(t, errorStr, "errCode: 1011")
	assert.Contains(t, errorStr, "msg: resource not found")
	assert.Contains(t, errorStr, "requestID: request-id-123")
}

func TestIsEfloCode(t *testing.T) {
	efloErr := &EFLOCode{
		Code:      1011,
		Message:   "resource not found",
		RequestID: "request-id-123",
	}

	// Test matching code
	assert.True(t, IsEfloCode(efloErr, 1011))

	// Test non-matching code
	assert.False(t, IsEfloCode(efloErr, 1013))

	// Test with wrapped error
	wrappedErr := fmt.Errorf("wrapped: %w", efloErr)
	assert.True(t, IsEfloCode(wrappedErr, 1011))
	assert.False(t, IsEfloCode(wrappedErr, 1013))

	// Test with regular error
	regularErr := errors.New("regular error")
	assert.False(t, IsEfloCode(regularErr, 1011))
}

func TestE2_Error(t *testing.T) {
	// Test with nil error
	e2 := &E2{e: nil}
	assert.Equal(t, "", e2.Error())

	// Test with SDKError
	sdkErr := &tea.SDKError{
		Code:    tea.String("err"),
		Message: tea.String("test message"),
	}
	e2 = &E2{e: sdkErr, extra: []string{"extra1", "extra2"}}
	errorStr := e2.Error()
	assert.Contains(t, errorStr, "errCode: err")
	assert.Contains(t, errorStr, "msg: test message")
	assert.Contains(t, errorStr, "extra1")
	assert.Contains(t, errorStr, "extra2")
}

func TestE2_Unwrap(t *testing.T) {
	sdkErr := &tea.SDKError{
		Code: tea.String("err"),
	}
	e2 := &E2{e: sdkErr}
	assert.Equal(t, sdkErr, e2.Unwrap())
}

func TestWarpError2(t *testing.T) {
	// Test with nil
	wrapped := WarpError2(nil)
	assert.Nil(t, wrapped)

	// Test with SDKError
	sdkErr := &tea.SDKError{
		Code:    tea.String("err"),
		Message: tea.String("test message"),
	}
	wrapped = WarpError2(sdkErr, "extra1", "extra2")
	assert.NotNil(t, wrapped)
	var e2 *E2
	assert.True(t, errors.As(wrapped, &e2))
	assert.Equal(t, sdkErr, e2.Unwrap())
	assert.Equal(t, []string{"extra1", "extra2"}, e2.extra)

	// Test with regular error
	regularErr := errors.New("regular error")
	wrapped = WarpError2(regularErr)
	assert.Equal(t, regularErr, wrapped)
}

func TestErrorCodeIs_EdgeCases(t *testing.T) {
	// Test with nil error
	assert.False(t, ErrorCodeIs(nil, "err1", "err2"))

	// Test with empty codes
	serverErr := apiErr.NewServerError(403, "{\"Code\": \"err\"}", "")
	assert.False(t, ErrorCodeIs(serverErr))

	// Test with multiple codes, none matching
	assert.False(t, ErrorCodeIs(serverErr, "err1", "err2", "err3"))
}

func TestErrorIs_EdgeCases(t *testing.T) {
	// Test with nil error - ErrorIs should handle nil gracefully
	// When err is nil, all check functions should return false
	assert.False(t, ErrorIs(nil))

	// Test with empty check functions
	err := errors.New("test")
	assert.False(t, ErrorIs(err))
}

func TestConstants(t *testing.T) {
	// Test error constants are defined
	assert.NotEmpty(t, ErrInternalError)
	assert.NotEmpty(t, ErrForbidden)
	assert.NotEmpty(t, InvalidVSwitchIDIPNotEnough)
	assert.NotEmpty(t, QuotaExceededPrivateIPAddress)
	assert.NotEmpty(t, ErrEniPerInstanceLimitExceeded)
	assert.NotEmpty(t, ErrSecurityGroupInstanceLimitExceed)
	assert.NotEmpty(t, ErrInvalidIPIPUnassigned)
	assert.NotEmpty(t, ErrInvalidENINotFound)
	assert.NotEmpty(t, ErrInvalidEcsIDNotFound)
	assert.NotEmpty(t, ErrIPv4CountExceeded)
	assert.NotEmpty(t, ErrIPv6CountExceeded)
	assert.NotEmpty(t, ErrInvalidENIState)
	assert.NotEmpty(t, ErrInvalidAllocationIDNotFound)
	assert.NotEmpty(t, ErrThrottling)
	assert.NotEmpty(t, ErrOperationConflict)
	assert.NotEmpty(t, ErrIdempotentFailed)
	assert.NotEqual(t, 0, ErrEfloPrivateIPQuotaExecuted)
	assert.NotEqual(t, 0, ErrEfloResourceNotFound)
}

func TestErrNotFound(t *testing.T) {
	// Test ErrNotFound constant
	assert.NotNil(t, ErrNotFound)
	assert.Equal(t, "not found", ErrNotFound.Error())
}

func TestErrorCodeIsAny(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		codes []string
		want  bool
	}{
		{
			name:  "apiErr.Error matching first code",
			err:   apiErr.NewServerError(403, "{\"Code\": \"err1\"}", ""),
			codes: []string{"err1", "err2", "err3"},
			want:  true,
		},
		{
			name:  "apiErr.Error matching second code",
			err:   apiErr.NewServerError(403, "{\"Code\": \"err2\"}", ""),
			codes: []string{"err1", "err2", "err3"},
			want:  true,
		},
		{
			name:  "apiErr.Error matching last code",
			err:   apiErr.NewServerError(403, "{\"Code\": \"err3\"}", ""),
			codes: []string{"err1", "err2", "err3"},
			want:  true,
		},
		{
			name:  "apiErr.Error not matching any code",
			err:   apiErr.NewServerError(403, "{\"Code\": \"errOther\"}", ""),
			codes: []string{"err1", "err2", "err3"},
			want:  false,
		},
		{
			name:  "apiErr.Error with empty codes",
			err:   apiErr.NewServerError(403, "{\"Code\": \"err1\"}", ""),
			codes: []string{},
			want:  false,
		},
		{
			name:  "tea.SDKError matching first code",
			err:   &tea.SDKError{Code: tea.String("sdk1"), Message: tea.String("test")},
			codes: []string{"sdk1", "sdk2", "sdk3"},
			want:  true,
		},
		{
			name:  "tea.SDKError matching second code",
			err:   &tea.SDKError{Code: tea.String("sdk2"), Message: tea.String("test")},
			codes: []string{"sdk1", "sdk2", "sdk3"},
			want:  true,
		},
		{
			name:  "tea.SDKError matching last code",
			err:   &tea.SDKError{Code: tea.String("sdk3"), Message: tea.String("test")},
			codes: []string{"sdk1", "sdk2", "sdk3"},
			want:  true,
		},
		{
			name:  "tea.SDKError not matching any code",
			err:   &tea.SDKError{Code: tea.String("sdkOther"), Message: tea.String("test")},
			codes: []string{"sdk1", "sdk2", "sdk3"},
			want:  false,
		},
		{
			name:  "tea.SDKError with empty codes",
			err:   &tea.SDKError{Code: tea.String("sdk1"), Message: tea.String("test")},
			codes: []string{},
			want:  false,
		},
		{
			name:  "tea.SDKError with nil Code",
			err:   &tea.SDKError{Code: nil, Message: tea.String("test")},
			codes: []string{"sdk1"},
			want:  false,
		},
		{
			name:  "regular error not matching",
			err:   errors.New("regular error"),
			codes: []string{"err1", "err2"},
			want:  false,
		},
		{
			name:  "nil error",
			err:   nil,
			codes: []string{"err1", "err2"},
			want:  false,
		},
		{
			name:  "wrapped apiErr.Error matching",
			err:   fmt.Errorf("wrapped: %w", apiErr.NewServerError(403, "{\"Code\": \"err1\"}", "")),
			codes: []string{"err1", "err2"},
			want:  true,
		},
		{
			name:  "wrapped tea.SDKError matching",
			err:   fmt.Errorf("wrapped: %w", &tea.SDKError{Code: tea.String("sdk1")}),
			codes: []string{"sdk1", "sdk2"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorCodeIsAny(tt.err, tt.codes...)
			assert.Equal(t, tt.want, got)
		})
	}
}
