package daemon

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/AliyunContainerService/terway/tc"
	"github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/denverdino/aliyungo/metadata"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"io/ioutil"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net"
	"os"
)

type ENIService struct {
	pool        *Pool
	client      *kubernetes.Clientset
	podCidr     *net.IPNet
	serviceCidr *net.IPNet
	tcEgress    tc.TC
	tcIngress   map[string]tc.TC
}

func (eni *ENIService) initialize() error {
	var err error
	//todo egress interface be configurable
	eni.tcEgress, err = tc.NewSystem("eth0", tc.DIRECTION_EGRESS)
	if err != nil {
		return err
	}

	eni.tcIngress = make(map[string]tc.TC)
	return nil //setupIPMasq()
}

func newCmdArgs(args *skel.CmdArgs) (*CmdArgs, error) {
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return nil, fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	return &CmdArgs{
		ContainerID: args.ContainerID,
		IfName:      args.IfName,
		Args:        args.Args,
		Path:        args.Path,
		Netns:       netns,
		PodName:     getPodArgsFromArgs(args.Args)[K8S_POD_NAME_ARGS],
		PodNS:       getPodArgsFromArgs(args.Args)[K8S_POD_NAMESPACE_ARGS],
	}, nil
}

func (eni *ENIService) Allocate(skelArgs *skel.CmdArgs, result *current.Result) error {
	args, err := newCmdArgs(skelArgs)
	if err != nil {
		return err
	}
	defer args.Netns.Close()

	log.Infof("get cni create request: %+v, %+v", skelArgs, args)

	podArgs := getPodArgsFromArgs(args.Args)

	podID, ok := podArgs[K8S_POD_NAME_ARGS]
	if !ok {
		return fmt.Errorf("error get pod id from cni args: %s", args)
	}

	podNs, ok := podArgs[K8S_POD_NAMESPACE_ARGS]
	if !ok {
		return fmt.Errorf("error get pod namespace from cni args: %s", args)
	}

	podInfo, err := eni.getPodInfo(podNs, podID)
	if err != nil {
		return err
	}

	if podInfo.useENI {
		if eni.pool == nil {
			return fmt.Errorf("eci pool disable due to pool initial failed")
		}
		nic, err := eni.pool.Allocate()
		if err != nil {
			return err
		}
		eniPlugin := &ENIPlugin{ENI: nic}
		if err := eniPlugin.Add(args, result); err != nil {
			return err
		}

		args.IfName = "veth1" //TODO 改成常量
		vethPlugin := &VETHPlugin{
			ENI:            nic,
			ServiceAddress: eni.serviceCidr,
			Subnet:         eni.podCidr,
		}
		if err := vethPlugin.Add(args, result); err != nil {
			return err
		}
	} else {
		vethPlugin := &VETHPlugin{
			ServiceAddress: eni.serviceCidr,
			Subnet:         eni.podCidr,
		}
		if err := vethPlugin.Add(args, result); err != nil {
			return err
		}

		log.Debugf("veth add result:%+v", result)

		if podInfo.tcEgress != "" {
			tcEgressRule := tc.BuildRule(result.IPs[0].Address.IP, tc.GenerateRuleID(tc.DIRECTION_EGRESS,
				normalizePodID(args.PodNS, args.PodName)), podInfo.tcEgress)
			tc.Addrule(tcEgressRule, eni.tcEgress)
		}

		if podInfo.tcIngress != "" {
			podInterface := VethNameForWorkload(normalizePodID(args.PodNS, args.PodName))
			tcCtr, ok := eni.tcIngress[podInterface]
			if !ok {
				tcCtr, err = tc.NewSystem(podInterface, tc.DIRECTION_INGRESS)
				if err != nil {
					return err
				}
				eni.tcIngress[podInterface] = tcCtr
			}
			tcIngressRule := tc.BuildRule(result.IPs[0].Address.IP, tc.GenerateRuleID(tc.DIRECTION_INGRESS,
				normalizePodID(args.PodNS, args.PodName)), podInfo.tcIngress)
			err := tcCtr.AddRule(tcIngressRule)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type podInfo struct {
	ip        string
	useENI    bool
	tcIngress string
	tcEgress  string
}

func (eni *ENIService) randomVethName() (string, error) {
	entropy := make([]byte, 4)
	_, err := rand.Reader.Read(entropy)
	if err != nil {
		return "", fmt.Errorf("failed to generate random veth name: %v", err)
	}

	return fmt.Sprintf("eni%x", entropy), nil
}

func GetMacAddress(ifName string, netns ns.NetNS) (string, error) {
	var mac string
	err := netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to get interface by name %s: %v", ifName, err)
		}
		mac = link.Attrs().HardwareAddr.String()
		return nil
	})
	return mac, err

}

func (eni *ENIService) Release(skelArgs *skel.CmdArgs, result *current.Result) error {
	if skelArgs.Netns == "" {
		return nil
	}
	args, err := newCmdArgs(skelArgs)
	log.Infof("get cni remove request: %+v, %+v", skelArgs, string(skelArgs.StdinData))
	if err != nil {
		return err
	}
	defer args.Netns.Close()

	podArgs := getPodArgsFromArgs(args.Args)

	podID, ok := podArgs[K8S_POD_NAME_ARGS]
	if !ok {
		return fmt.Errorf("error get pod id from cni args: %s", args)
	}

	podNs, ok := podArgs[K8S_POD_NAMESPACE_ARGS]
	if !ok {
		return fmt.Errorf("error get pod namespace from cni args: %s", args)
	}

	podInfo, err := eni.getPodInfo(podNs, podID)
	if err != nil {
		return err
	}

	if podInfo.useENI {
		if eni.pool == nil {
			return fmt.Errorf("eci pool disable due to pool initial failed")
		}

		eniName, err := eni.randomVethName()
		if err != nil {
			return err
		}

		mac, err := GetMacAddress(args.IfName, args.Netns)
		if err != nil {
			return err
		}
		eniPlugin := &ENIPlugin{
			ENI: &types.ENI{
				Name: eniName,
			},
		}

		if err := eniPlugin.Del(args, result); err != nil {
			return err
		}

		if err := eni.pool.Release(mac); err != nil {
			return err
		}
	} else {

		vethPlugin := &VETHPlugin{
			Subnet: eni.podCidr,
		}
		if err := vethPlugin.Del(skelArgs, result); err != nil {
			return err
		}
	}

	return nil
}

//对于configure里没有配置的，填充对应的默认值
func setDefault(cfg *types.Configure) error {
	if cfg.Prefix == "" {
		cfg.Prefix = "eth"
	}

	//if cfg.MinPoolSize == 0 {
	//	cfg.MinPoolSize = 2
	//}

	if cfg.MaxPoolSize == 0 {
		cfg.MaxPoolSize = 5
	}

	if cfg.HotPlug == "" {
		cfg.HotPlug = "true"
	}

	if cfg.HotPlug == "false" || cfg.HotPlug == "0" {
		cfg.HotPlug = "false"
	}

	//cfg.AccessId = os.Getenv("AK_ID")
	//cfg.AccessSecret = os.Getenv("AK_SECRET")
	//if cfg.AccessSecret == "" || cfg.AccessId == "" {
	//	return errors.New("AK_ID & AK_SECRET can not be empty")
	//}

	return nil
}

func validateConfig(cfg *types.Configure) error {
	return nil
}

func createPool(cfg *types.Configure) (*Pool, error) {
	pc := &types.PoolConfig{
		SecurityGroup: cfg.SecurityGroup,
		MaxPoolSize:   cfg.MaxPoolSize,
		MinPoolSize:   cfg.MinPoolSize,
		AccessId:      cfg.AccessId,
		AccessSecret:  cfg.AccessSecret,
		HotPlug:       cfg.HotPlug == "true",
	}
	meta := metadata.NewMetaData(nil)
	zone, err := meta.Zone()
	if err != nil {
		return nil, err
	}
	if cfg.VSwitches != nil {
		pc.VSwitch = cfg.VSwitches[zone]
	}
	if pc.VSwitch == "" {
		if pc.VSwitch, err = meta.VswitchID(); err != nil {
			return nil, err
		}
	}

	if pc.Region, err = meta.Region(); err != nil {
		return nil, err
	}

	if pc.VPC, err = meta.VpcID(); err != nil {
		return nil, err
	}

	if pc.InstanceID, err = meta.InstanceID(); err != nil {
		return nil, err
	}

	return newPool(pc)
}

func newENIService(configFilePath string) (*ENIService, error) {
	f, err := os.Open(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed open file %s: %v", configFilePath, err)
	}
	defer f.Close()

	eni := &ENIService{}

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed read file %s: %v", configFilePath, err)
	}
	cfg := &types.Configure{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed parse config: %v", err)
	}

	log.Debugf("got eni config: %+v from: %+v", cfg, configFilePath)

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	if err := setDefault(cfg); err != nil {
		return nil, err
	}

	if eni.pool, err = createPool(cfg); err != nil {
		log.Errorf("error initial eni pool, eni pool will be disabled, error: %v", err)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	if eni.client, err = kubernetes.NewForConfig(config); err != nil {
		return nil, err
	}

	if eni.podCidr, err = eni.podCIDR(); err != nil {
		return nil, err
	}

	if cfg.ServiceCIDR == "" {
		if cfg.ServiceCIDR, err = eni.getSvcCidr(); err != nil {
			return nil, err
		}
	}
	if _, eni.serviceCidr, err = net.ParseCIDR(cfg.ServiceCIDR); err != nil {
		return nil, err
	}

	return eni, nil
}
