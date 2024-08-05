package credential

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientMgr_refreshToken(t *testing.T) {
	mgr, err := NewClientMgr("foo", &AKPairProvider{
		accessKeyID:     "foo",
		accessKeySecret: "bar",
	})
	assert.NoError(t, err)
	ok, err := mgr.refreshToken()
	assert.True(t, ok)
	assert.NoError(t, err)

	assert.Equalf(t, "vpc", mgr.ecs.Network, "default endpoint should be vpc")
}
