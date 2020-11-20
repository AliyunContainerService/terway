package aliyun

import (
	"errors"
	"testing"

	"github.com/denverdino/aliyungo/common"
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
				err: &common.Error{
					ErrorResponse: common.ErrorResponse{
						Code: "err",
					},
					StatusCode: 403,
				},
			},
			want: true,
		}, {
			name: "code not match",
			args: args{
				errCode: "errNotMatch",
				err: &common.Error{
					ErrorResponse: common.ErrorResponse{
						Code: "err",
					},
					StatusCode: 403,
				},
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
