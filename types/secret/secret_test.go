package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringReturnsMaskedValue(t *testing.T) {
	s := Secret("mysecret")
	assert.Equal(t, "******", s.String())
	assert.Equal(t, "mysecret", string((s)))
}

func TestGoStringReturnsMaskedValue(t *testing.T) {
	s := Secret("mysecret")
	assert.Equal(t, "******", s.GoString())
}

func TestMarshalJSONReturnsMaskedValue(t *testing.T) {
	s := Secret("mysecret")
	json, err := s.MarshalJSON()
	assert.NoError(t, err)
	assert.Equal(t, `"******"`, string(json))
}

func TestMarshalJSONHandlesEmptySecret(t *testing.T) {
	s := Secret("")
	json, err := s.MarshalJSON()
	assert.NoError(t, err)
	assert.Equal(t, `"******"`, string(json))
}
