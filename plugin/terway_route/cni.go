package main

import (
	"github.com/AliyunContainerService/terway/version"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"

	"fmt"
	"github.com/containernetworking/cni/pkg/types"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"net/rpc"
	//"path/filepath"
)

const DEFAULT_SOCKET_PATH = "/var/run/eni/eni.socket"

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.GetSpecVersionSupported())
}

func cmdAdd(args *skel.CmdArgs) error {
	versionDecoder := &cniversion.ConfigDecoder{}
	confVersion, err := versionDecoder.Decode(args.StdinData)
	if err != nil {
		return err
	}

	result := &current.Result{}
	if err := rpcCall("ENIService.Allocate", args, result); err != nil {
		return err
	}

	return types.PrintResult(result, confVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	result := struct{}{}
	if err := rpcCall("ENIService.Release", args, &result); err != nil {
		return err
	}
	return nil
}

func rpcCall(method string, args *skel.CmdArgs, result interface{}) error {
	client, err := rpc.DialHTTP("unix", DEFAULT_SOCKET_PATH)
	if err != nil {
		return fmt.Errorf("error dialing ENIService daemon: %v", err)
	}

	//// The daemon may be running under a different working dir
	//// so make sure the netns path is absolute.
	//netns, err := filepath.Abs(args.Netns)
	//if err != nil {
	//	return fmt.Errorf("failed to make %q an absolute path: %v", args.Netns, err)
	//}
	//args.Netns = netns

	err = client.Call(method, args, result)
	if err != nil {
		return fmt.Errorf("error calling %v: %v", method, err)
	}

	return nil
}
