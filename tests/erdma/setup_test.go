//go:build e2e

package erdma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
)

var (
	testenv   env.Environment
	namespace string
	skipTest  bool
)

const (
	podName = "pod-use-erdma"
)

func TestMain(m *testing.M) {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("error get home path")
	}

	envCfg := envconf.NewWithKubeConfig(filepath.Join(home, ".kube", "config")).
		WithRandomNamespace()

	testenv = env.NewWithConfig(envCfg)

	_ = clientgoscheme.AddToScheme(scheme.Scheme)

	testenv.Setup(
		envfuncs.CreateNamespace(envCfg.Namespace()),
		func(ctx context.Context, config *envconf.Config) (context.Context, error) {
			// patch terway to support erdma
			terwayCM := &corev1.ConfigMap{}
			err := config.Client().Resources().Get(ctx, "eni-config", "kube-system", terwayCM)
			if err != nil {
				return nil, err
			}
			namespace = envCfg.Namespace()
			terwayCfg, ok := terwayCM.Data["eni_conf"]
			if !ok {
				return nil, fmt.Errorf("eni_conf not found")
			}
			terwayCfgMap := make(map[string]json.RawMessage)
			err = json.Unmarshal([]byte(terwayCfg), &terwayCfgMap)
			if err != nil {
				return nil, err
			}
			terwayCfgMap["enable_erdma"] = json.RawMessage(`true`)
			update, err := json.Marshal(terwayCfgMap)
			if err != nil {
				return nil, err
			}
			terwayCM.Data["eni_conf"] = string(update)
			err = config.Client().Resources().Update(ctx, terwayCM)
			if err != nil {
				return nil, err
			}
			terwayList := &corev1.PodList{}
			err = config.Client().Resources().WithNamespace("kube-system").List(ctx, terwayList, func(options *metav1.ListOptions) {
				options.LabelSelector = "app=terway-eniip"
			})
			if err != nil {
				return nil, err
			}
			if len(terwayList.Items) == 0 {
				skipTest = true
				return ctx, nil
			}

			for _, pod := range terwayList.Items {
				// restart terway
				config.Client().Resources().WithNamespace("kube-system").Delete(ctx, &pod)
			}
			time.Sleep(30 * time.Second)
			nodeList := &corev1.NodeList{}
			err = config.Client().Resources().List(ctx, nodeList)
			if err != nil {
				return nil, err
			}
			var nodeERDMA bool
			for _, node := range nodeList.Items {
				instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]
				if !ok {
					continue
				}
				if regexp.MustCompile("^ecs\\..*8.*\\..*$").Match([]byte(instanceType)) {
					nodeERDMA = true
				}
			}
			if !nodeERDMA {
				skipTest = true
				return ctx, nil
			}
			err = installDriversByOOS(ctx, config)
			if err != nil {
				err1 := installDrivers(ctx, config)
				if err1 != nil {
					err = fmt.Errorf("error install driver: oos: %v, exec: %v", err, err1)
				} else {
					err = nil
				}
			}
			if err != nil {
				return nil, err
			}

			return ctx, nil
		},
	)

	testenv.Finish(
		envfuncs.DeleteNamespace(envCfg.Namespace()),
	)

	os.Exit(testenv.Run(m))
}

func installDriversByOOS(ctx context.Context, config *envconf.Config) error {
	accessKey := os.Getenv("ACCESS_KEY_ID")
	accessKeySecret := os.Getenv("ACCESS_KEY_SECRET")
	regionID := os.Getenv("REGION")
	if accessKey == "" || accessKeySecret == "" || regionID == "" {
		return fmt.Errorf("no available credential config: len(ak):%v, len(aksecret):%v, %v", len(accessKey), len(accessKeySecret), regionID)
	}
	client, err := ecs.NewClientWithAccessKey(regionID, accessKey, accessKeySecret)
	if err != nil {
		return fmt.Errorf("error create ecs client: %v", err)
	}
	nodeList := &corev1.NodeList{}
	err = config.Client().Resources().List(ctx, nodeList)
	if err != nil {
		return fmt.Errorf("error list node: %v", err)
	}
	if len(nodeList.Items) == 0 {
		return fmt.Errorf("no available node")
	}

	request := ecs.CreateRunCommandRequest()
	request.Type = "RunShellScript"
	request.CommandContent =
		"curl -OL 'https://elastic-rdma.oss-cn-hangzhou.aliyuncs.com/wip/archive/erdma_installer_202311071611.tar.gz' &&  " +
			"tar -xzvf erdma_installer_202311071611.tar.gz && cd erdma_installer && " +
			"yum install -y gcc-c++ dkms cmake && ./install.sh --batch"
	request.Name = "installErdmaDriver"
	var instanceIDs []string
	for _, node := range nodeList.Items {
		providerID := strings.Split(node.Spec.ProviderID, ".")
		if len(providerID) != 2 {
			logrus.Warnf("error parse providerID: %v", node.Spec.ProviderID)
			continue
		}
		instanceIDs = append(instanceIDs, providerID[1])
	}
	if len(instanceIDs) == 0 {
		return fmt.Errorf("no available node")
	}
	if len(instanceIDs) > 100 {
		instanceIDs = instanceIDs[:100]
	}
	request.InstanceId = &instanceIDs
	request.Timeout = requests.NewInteger(600)
	response, err := client.RunCommand(request)
	if err != nil {
		return fmt.Errorf("error run command: %v", err)
	}
	var innerErr error
	err = wait.ExponentialBackoffWithContext(ctx, wait.Backoff{
		Duration: 1 * time.Minute,
		Steps:    10,
	}, func(ctx context.Context) (bool, error) {
		describeInvocationRequest := ecs.CreateDescribeInvocationsRequest()
		describeInvocationRequest.InvokeId = response.InvokeId
		resp, err := client.DescribeInvocations(describeInvocationRequest)
		if err != nil {
			return false, nil
		}
		done := true
		lo.ForEach(resp.Invocations.Invocation, func(invocation ecs.Invocation, _ int) {
			lo.ForEach(invocation.InvokeInstances.InvokeInstance, func(invocationInstance ecs.InvokeInstance, _ int) {
				if invocationInstance.InstanceInvokeStatus != "Finished" {
					done = false
				} else {
					if invocationInstance.ErrorCode != "0" {
						innerErr = fmt.Errorf("error run command: %+v", invocationInstance)
					}
				}
			})
		})
		if !done {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("error run command: %v, %v", err, innerErr)
	}
	return nil
}

func installDrivers(ctx context.Context, config *envconf.Config) error {
	csiList := &corev1.PodList{}
	err := config.Client().Resources().WithNamespace("kube-system").List(ctx, csiList, func(options *metav1.ListOptions) {
		options.LabelSelector = "app=csi-plugin"
	})
	if err != nil {
		return err
	}
	if len(csiList.Items) == 0 {
		return fmt.Errorf("not found csi plugin pod to install drivers")
	}

	errors := atomic.Error{}
	wg := sync.WaitGroup{}
	for _, pod := range csiList.Items {
		wg.Add(1)
		go func(podName string) {
			var stdout, stderr bytes.Buffer
			cmd := []string{"nsenter", "-t", "1", "-m", "--", "bash", "-c",
				"readlink /proc/self > /sys/fs/cgroup/cpu/tasks && readlink /proc/self > /sys/fs/cgroup/memory/tasks  &&" +
					"curl -OL 'https://elastic-rdma.oss-cn-hangzhou.aliyuncs.com/wip/archive/erdma_installer_202311071611.tar.gz' &&  " +
					"tar -xzvf erdma_installer_202311071611.tar.gz && cd erdma_installer && " +
					"yum install -y gcc-c++ dkms cmake && ./install.sh --batch"}
			err := config.Client().Resources().ExecInPod(ctx, "kube-system", podName, "csi-plugin", cmd, &stdout, &stderr)
			if err != nil {
				errors.Store(fmt.Errorf("error install command: %v,%s,%s", err, stdout.String(), stderr.String()))
			}
			wg.Done()
		}(pod.Name)
	}
	wg.Wait()
	if err = errors.Load(); err != nil {
		return err
	}
	return nil
}
