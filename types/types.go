package types

import (
	"encoding/json"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"net"
)

type NetConf struct {
	types.NetConf
	IP string `ip:"ip"` //占位，暂不支持。考虑使用pod annotation
}

func NewConf(args *skel.CmdArgs) (*NetConf, error) {
	var cfg NetConf
	if err := json.Unmarshal(args.StdinData, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type ENI struct {
	ID      string
	Name    string
	Address net.IPNet
	MAC     string
	Gateway net.IP
}

type TrafficShappingRule struct {
	ID        string
	Source    string
	Bandwidth string
	Classify  int
}
