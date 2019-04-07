package daemon

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"
	"github.com/containernetworking/plugins/plugins/ipam/host-local/backend/disk"
	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultPrefix   = "cali"
	defaultIpamPath = "/var/lib/cni/networks/"
)

type VethResourceManager struct {
	runtimeAPI containerRuntime
}

func (*VethResourceManager) Allocate(context *NetworkContext, prefer string) (types.NetworkResource, error) {
	return &types.Veth{
		HostVeth: link.VethNameForPod(context.pod.Name, context.pod.Namespace, defaultPrefix),
	}, nil
}

func (*VethResourceManager) Release(context *NetworkContext, resId string) error {
	return nil
}

func (f *VethResourceManager) GarbageCollection(inUseSet map[string]interface{}, expireResSet map[string]interface{}) error {
	// fixme do gc on cni binary
	lock, err := disk.NewFileLock(defaultIpamPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	err = lock.Lock()
	if err != nil {
		return err
	}
	sandboxList, err := f.runtimeAPI.GetRunningSandbox()
	if err != nil {
		return err
	}

	sandboxStubSet := make(map[string]interface{})
	for _, sandbox := range sandboxList {
		sandboxStubSet[sandbox] = struct{}{}
	}

	files, err := ioutil.ReadDir(defaultIpamPath)
	if err != nil {
		log.Errorf("Failed to list files in %q: %v", defaultIpamPath, err)
		return fmt.Errorf("failed to list files in %q: %v", defaultIpamPath, err)
	}

	// gather containerIDs for allocated ips
	ipContainerIdMap := make(map[string]string)
	for _, file := range files {
		// skip non checkpoint file
		if ip := net.ParseIP(file.Name()); ip == nil {
			continue
		}

		content, err := ioutil.ReadFile(filepath.Join(defaultIpamPath, file.Name()))
		if err != nil {
			log.Errorf("Failed to read file %v: %v", file, err)
		}
		ipContainerIdMap[file.Name()] = strings.TrimSpace(string(content))
	}

	for ip, containerId := range ipContainerIdMap {
		if _, ok := sandboxStubSet[containerId]; !ok && containerId != "" {
			log.Warnf("detect ip address leak: %s, removing", ip)
			err := os.Remove(filepath.Join(defaultIpamPath, ip))
			if err != nil {
				log.Errorf("error remove leak ip: %s, err: %v", ip, err)
			}
		}
	}
	return nil
}

func NewVPCResourceManager() (ResourceManager, error) {
	return &VethResourceManager{
		runtimeAPI: dockerRuntime{},
	}, nil
}

type containerRuntime interface {
	GetRunningSandbox() ([]string, error)
}

type dockerRuntime struct{}

func (dockerRuntime) GetRunningSandbox() ([]string, error) {
	var containerList []string
	dockerCli, err := client.NewClientWithOpts(
		client.WithVersion("v1.21"),
	)
	if err != nil {
		return containerList, fmt.Errorf("error init docker client to restore local lease: %+v", err)
	}
	defer dockerCli.Close()

	timeoutContext, _ := context.WithTimeout(context.Background(), time.Minute)
	listFilter := filters.NewArgs()
	listFilter.Add("label", fmt.Sprintf("%s=%s", "io.kubernetes.docker.type", "podsandbox"))
	sandboxContainer, err := dockerCli.ContainerList(timeoutContext,
		dockerTypes.ContainerListOptions{
			Filters: listFilter,
		},
	)
	if err != nil {
		return containerList, fmt.Errorf("error get docker containers to restore local lease: %+v", err)
	}

	for _, container := range sandboxContainer {
		timeoutContext, _ := context.WithTimeout(context.Background(), time.Minute)
		containerInfo, err := dockerCli.ContainerInspect(timeoutContext, container.ID)
		if err != nil {
			return containerList, fmt.Errorf("error get container info to cleanup: %+v", err)
		}
		if !containerInfo.State.Running {
			continue
		}
		if containerInfo.NetworkSettings == nil ||
			containerInfo.NetworkSettings.SandboxKey == "" ||
			containerInfo.NetworkSettings.SandboxKey == "/var/run/docker/netns/default" {
			continue
		}

		log.Debugf("get container for ipam gc: %+v", container.Labels)
		containerList = append(containerList, container.ID)
	}
	return containerList, nil
}
