package main

import (
	"testing"

	"github.com/Jeffail/gabs/v2"
	"github.com/stretchr/testify/assert"
)

func Test_mergeConfigList(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar"
        }`), []byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
}

func Test_mergeConfigList_ipvl(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`), []byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.2.type").Data())
	assert.Equal(t, "ipvlan", g.Path("plugins.0.eniip_virtual_type").Data())
}

func Test_mergeConfigList_ipvl_exist(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "edt", g.Path("plugins.0.bandwidth_mode").Data())
	assert.Equal(t, "bar", g.Path("plugins.0.foo").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "ipvlan", g.Path("plugins.1.datapath").Data())
}

func Test_mergeConfigList_ipvl_unsupport(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return false
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: false,
		EDT:  false,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, false, g.ExistsP("plugins.0.eniip_virtual_type"))
	assert.Equal(t, "portmap", g.Path("plugins.1.type").Data())
}

func Test_mergeConfigList_migrate_datapathv2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
            "type":"terway",
            "foo":"bar",
            "eniip_virtual_type": "ipvlan"
        }`),
		[]byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
            "type":"portmap",
            "capabilities":{
                "portMappings":true
            },
            "externalSetMarkChain":"KUBE-MARK-MASQ"
        }`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.1.datapath").Data())
	assert.Equal(t, "portmap", g.Path("plugins.2.type").Data())
}

func Test_mergeConfigList_datapathv2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
			"eniip_virtual_type": "datapathv2"
		}`), []byte(`{
            "type":"cilium-cni"
        }`),
		[]byte(`{
			"type":"portmap",
			"capabilities":{
				"portMappings":true
			},
			"externalSetMarkChain":"KUBE-MARK-MASQ"
		}`)}, &feature{
		EBPF: true,
		EDT:  true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
	assert.Equal(t, "datapathv2", g.Path("plugins.1.datapath").Data())
	assert.Equal(t, "portmap", g.Path("plugins.2.type").Data())
}

func TestVeth(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "veth", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, 1, len(g.Path("plugins").Children()))
}

func TestVethWithNoPolicy(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
            "network_policy_provider": "ebpf"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: false,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, "veth", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, 1, len(g.Path("plugins").Children()))
}

func TestVethToDatapathV2(t *testing.T) {
	_switchDataPathV2 = func() bool {
		return true
	}
	out, err := mergeConfigList([][]byte{
		[]byte(`{
			"type":"terway",
			"foo":"bar",
            "network_policy_provider": "ebpf"
		}`)}, &feature{
		EBPF:                true,
		EDT:                 true,
		EnableNetworkPolicy: true,
	})
	assert.NoError(t, err)

	g, err := gabs.ParseJSON([]byte(out))
	assert.NoError(t, err)

	assert.Equal(t, "terway", g.Path("plugins.0.type").Data())
	assert.Equal(t, 2, len(g.Path("plugins").Children()))
	assert.Equal(t, "datapathv2", g.Path("plugins.0.eniip_virtual_type").Data())
	assert.Equal(t, "cilium-cni", g.Path("plugins.1.type").Data())
}
