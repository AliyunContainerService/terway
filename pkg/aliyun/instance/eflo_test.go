package instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEFLO(t *testing.T) {
	e := EFLO{
		f: &efloConfig{
			RegionID:     "a",
			ZoneID:       "b",
			NodeID:       "c",
			InstanceType: "d",
			AckNicName:   "e",
		},
	}
	regionID, err := e.GetRegionID()
	assert.Nil(t, err)
	assert.Equal(t, "a", regionID)

	zoneID, err := e.GetZoneID()
	assert.Nil(t, err)
	assert.Equal(t, "b", zoneID)

	nodeID, err := e.GetInstanceID()
	assert.Nil(t, err)
	assert.Equal(t, "c", nodeID)

	instanceType, err := e.GetInstanceType()
	assert.Nil(t, err)
	assert.Equal(t, "d", instanceType)
}
