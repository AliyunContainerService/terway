package errors

import (
	"errors"
	"testing"

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
