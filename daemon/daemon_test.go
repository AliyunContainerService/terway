package daemon

import (
	"testing"

	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/types"
	"github.com/stretchr/testify/assert"
)

func Test_toResMapping(t *testing.T) {
	pool := tracing.FakeResourcePoolStats{
		Local: map[string]types.Res{
			"idle": &types.FakeRes{
				ID:     "idle",
				Type:   "",
				Status: types.ResStatusIdle,
			}, "inuse": &types.FakeRes{
				ID:     "inuse",
				Type:   "",
				Status: types.ResStatusInUse,
			}, "invalid-remote": &types.FakeRes{
				ID:     "invalid-remote",
				Type:   "",
				Status: types.ResStatusInvalid,
			},
		},
		Remote: map[string]types.Res{
			"idle": &types.FakeRes{
				ID:     "idle",
				Type:   "",
				Status: types.ResStatusIdle,
			},
			"inuse": &types.FakeRes{
				ID:     "inuse",
				Type:   "",
				Status: types.ResStatusInUse,
			}, "invalid-lo": &types.FakeRes{
				ID:     "invalid-lo",
				Type:   "",
				Status: types.ResStatusInvalid,
			},
		},
	}
	pods := []types.PodResources{
		{
			PodInfo: &types.PodInfo{
				Name:      "inuse",
				Namespace: "inuse",
			},
			Resources: []types.ResourceItem{{
				Type: "",
				ID:   "inuse",
			}},
		},
		{
			PodInfo: &types.PodInfo{
				Name:      "invalid-remote",
				Namespace: "invalid-remote",
			},
			Resources: []types.ResourceItem{{
				Type: "",
				ID:   "invalid-remote",
			}},
		},
		{
			PodInfo: &types.PodInfo{
				Name:      "invalid-lo",
				Namespace: "invalid-lo",
			},
			Resources: []types.ResourceItem{{
				Type: "",
				ID:   "invalid-lo",
			}},
		},
	}
	list := []interface{}{}
	for _, p := range pods {
		list = append(list, p)
	}
	mapping, err := toResMapping(&pool, list)
	assert.NoError(t, err)
	for _, m := range mapping {
		switch m.Name {
		case "inuse", "idle":
			assert.True(t, m.Valid)
		case "invalid-remote", "invalid-lo":
			assert.False(t, m.Valid)
		}
	}
}
