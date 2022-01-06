package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	"github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/plugin/driver/types"
	"github.com/AliyunContainerService/terway/rpc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func getCmdArgs(args *skel.CmdArgs) (*cniCmdArgs, error) {
	var err error

	var conf types.CNIConf
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return nil, fmt.Errorf("error parse args, %w", err)
	}
	if conf.MTU == 0 {
		conf.MTU = defaultMTU
	}

	var k8sArgs types.K8SArgs
	if err = cniTypes.LoadArgs(args.Args, &k8sArgs); err != nil {
		return nil, fmt.Errorf("error parse args, %w", err)
	}

	return &cniCmdArgs{
		conf:      &conf,
		k8sArgs:   &k8sArgs,
		inputArgs: args,
	}, nil
}

type cniCmdArgs struct {
	conf      *types.CNIConf
	k8sArgs   *types.K8SArgs
	inputArgs *skel.CmdArgs
}

func (args *cniCmdArgs) GetCNIConf() *types.CNIConf {
	if args == nil || args.conf == nil {
		return nil
	}
	return args.conf
}

func (args *cniCmdArgs) GetK8SConfig() *types.K8SArgs {
	if args == nil || args.k8sArgs == nil {
		return nil
	}
	return args.k8sArgs
}

func (args *cniCmdArgs) GetInputArgs() *skel.CmdArgs {
	if args == nil {
		return nil
	}
	return args.inputArgs
}

func (args *cniCmdArgs) GetNetNSPath() string {
	return ""
}

func (args *cniCmdArgs) Close() error {
	return nil
}

func isNSPathNotExist(err error) bool {
	return false
}

func doCmdAdd(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) (containerIPNet *terwayTypes.IPNetSet, gatewayIPSet *terwayTypes.IPSet, err error) {
	var k8sConfig, args = cmdArgs.k8sArgs, cmdArgs.inputArgs

	defer func() {
		eventCtx, cancel := context.WithTimeout(ctx, defaultEventTimeout)
		defer cancel()
		if err != nil {
			_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
				EventTarget:     rpc.EventTarget_EventTargetPod,
				K8SPodName:      string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
				EventType:       rpc.EventType_EventTypeWarning,
				Reason:          "AllocIPFailed",
				Message:         err.Error(),
			})
		} else {
			_, _ = client.RecordEvent(eventCtx, &rpc.EventRequest{
				EventTarget:     rpc.EventTarget_EventTargetPod,
				K8SPodName:      string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace: string(k8sConfig.K8S_POD_NAMESPACE),
				EventType:       rpc.EventType_EventTypeNormal,
				Reason:          "AllocIPSucceed",
				Message:         fmt.Sprintf("Alloc IP %s", containerIPNet.String()),
			})
		}
	}()

	allocResult, err := client.AllocIP(ctx, &rpc.AllocIPRequest{
		Netns:                  args.Netns,
		K8SPodName:             string(k8sConfig.K8S_POD_NAME),
		K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
		K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
		IfName:                 args.IfName,
	})
	if err != nil {
		err = fmt.Errorf("cmdAdd: error alloc ip %w", err)
		return
	}
	if !allocResult.Success {
		err = fmt.Errorf("cmdAdd: alloc ip return not success")
		return
	}

	defer func() {
		if err != nil {
			releaseCtx, cancel := context.WithTimeout(context.Background(), defaultCniTimeout)
			defer cancel()
			_, _ = client.ReleaseIP(releaseCtx, &rpc.ReleaseIPRequest{
				K8SPodName:             string(k8sConfig.K8S_POD_NAME),
				K8SPodNamespace:        string(k8sConfig.K8S_POD_NAMESPACE),
				K8SPodInfraContainerId: string(k8sConfig.K8S_POD_INFRA_CONTAINER_ID),
				Reason:                 fmt.Sprintf("roll back ip for error: %v", err),
			})
		}
	}()

	return
}

func doCmdDel(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	return nil
}

func doCmdCheck(ctx context.Context, logger *logrus.Entry, client rpc.TerwayBackendClient, cmdArgs *cniCmdArgs) error {
	return nil
}
