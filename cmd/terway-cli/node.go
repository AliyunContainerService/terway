package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/pkg/utils/nodecap"
	"github.com/AliyunContainerService/terway/pkg/version"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

const eniOnlyCNI = `{
  "cniVersion": "0.4.0",
  "name": "terway-chainer",
  "plugins": [
    {
      "capabilities": {
        "bandwidth": true
      },
      "host_stack_cidrs": [
        "169.254.20.10/32"
      ],
      "type": "terway"
    }
  ]
}`

const cniFilePath = "/etc/cni/net.d/10-terway.conflist"
const nodeCapabilitiesFile = "/var/run/eni/node_capabilities"

type Task struct {
	Name string
	Func func(cmd *cobra.Command, args []string) error
}

var tasks = []Task{
	{
		Name: "get eni config",
		Func: getENIConfig,
	},
	{
		Name: "eniOnly",
		Func: overrideCNI,
	},
	{
		Name: "dual stack",
		Func: dualStack,
	},
}

var nodeconfigCmd = &cobra.Command{
	Use:          "nodeconfig",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		for _, task := range tasks {
			err := task.Func(cmd, args)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "task: %serror: %v\n", task.Name, err)
				os.Exit(1)
			}
		}
	},
}

var eniCfg *daemon.Config

func getENIConfig(cmd *cobra.Command, args []string) error {
	var err error
	eniCfg, err = daemon.GetConfigFromFileWithMerge("/etc/eni/eni.json", nil)
	if err != nil {
		return err
	}

	fmt.Printf("eni config: %+v\n", eniCfg)
	return nil
}

func overrideCNI(cmd *cobra.Command, args []string) error {
	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = version.UA
	c, err := k8sClient.New(restConfig, k8sClient.Options{
		Scheme: types.Scheme,
		Mapper: types.NewRESTMapper(),
	})
	if err != nil {
		return err
	}

	node := &corev1.Node{}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	nodeName := os.Getenv("K8S_NODE_NAME")

	err = c.Get(ctx, k8sClient.ObjectKey{
		Name: nodeName,
	}, node, &k8sClient.GetOptions{
		Raw: &metav1.GetOptions{
			ResourceVersion: "0",
		},
	})
	if err != nil {
		return fmt.Errorf("get node %s error: %w", nodeName, err)
	}

	store := nodecap.NewFileNodeCapabilities(nodeCapabilitiesFile)
	return setExclusiveMode(store, node.Labels, cniFilePath)
}

func setExclusiveMode(store nodecap.NodeCapabilitiesStore, labels map[string]string, cniPath string) error {
	err := store.Load()
	if err != nil {
		return err
	}
	now := types.NodeExclusiveENIMode(labels)

	switch store.Get(nodecap.NodeCapabilityExclusiveENI) {
	case string(types.ExclusiveENIOnly):
		if now != types.ExclusiveENIOnly {
			return fmt.Errorf("exclusive eni mode changed")
		}
	case string(types.ExclusiveDefault):
		if now != types.ExclusiveDefault {
			return fmt.Errorf("exclusive eni mode changed")
		}
	case "":
		// empty for new node, or rebooted
	}

	store.Set(nodecap.NodeCapabilityExclusiveENI, string(now))
	err = store.Save()
	if err != nil {
		return err
	}

	// write cni config
	if now == types.ExclusiveENIOnly {
		err = os.WriteFile(cniPath, []byte(eniOnlyCNI), 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

func dualStack(cmd *cobra.Command, args []string) error {
	store := nodecap.NewFileNodeCapabilities(nodeCapabilitiesFile)

	val := ""
	switch eniCfg.IPStack {
	case "dual", "ipv6":
		val = "true"
	default:
		val = "false"
	}

	err := store.Load()
	if err != nil {
		return err
	}

	store.Set(nodecap.NodeCapabilityIPv6, val)
	return store.Save()
}
