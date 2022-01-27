package errors

import (
	"errors"
	"testing"

	apiErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
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
