package route

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoute(t *testing.T) {
	r := Route{Dst: "10.0.0.0/8"}
	assert.Equal(t, "10.0.0.0/8", r.Dst)
}
