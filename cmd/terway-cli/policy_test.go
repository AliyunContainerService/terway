package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_extractArgs(t *testing.T) {
	type args struct {
		in string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "test1",
			args: args{
				in: "--foo=bar --baz=\"aa bb\"",
			},
			want: []string{"--foo=bar", "--baz=\"aa bb\""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, extractArgs(tt.args.in), "extractArgs(%v)", tt.args.in)
		})
	}
}
