package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_parseRelease(t *testing.T) {
	type args struct {
		rel string
	}
	tests := []struct {
		name      string
		args      args
		wantMajor int
		wantMinor int
		wantPatch int
		wantOk    bool
	}{
		{
			name: "test1",
			args: args{
				rel: "6.8.0-51-generic",
			},
			wantMajor: 6,
			wantMinor: 8,
			wantPatch: 0,
			wantOk:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMajor, gotMinor, gotPatch, gotOk := parseRelease(tt.args.rel)
			assert.Equalf(t, tt.wantMajor, gotMajor, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantMinor, gotMinor, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantPatch, gotPatch, "parseRelease(%v)", tt.args.rel)
			assert.Equalf(t, tt.wantOk, gotOk, "parseRelease(%v)", tt.args.rel)
		})
	}
}
