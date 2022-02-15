package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AliyunContainerService/terway/pkg/tracing"

	"github.com/containernetworking/plugins/plugins/ipam/host-local/backend/disk"
	dockerTypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"

	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"
)

const (
	defaultPrefix   = "cali"
	defaultIpamPath = "/var/lib/cni/networks/"
)

type vethResourceManager struct {
	runtimeAPI containerRuntime
}

func (*vethResourceManager) Allocate(context *networkContext, prefer string) (types.NetworkResource, error) {
	vethName, _ := link.VethNameForPod(context.pod.Name, context.pod.Namespace, "", defaultPrefix)
	return &types.Veth{
		HostVeth: vethName,
	}, nil
}

func (*vethResourceManager) Release(context *networkContext, resItem types.ResourceItem) error {
	return nil
}

func (f *vethResourceManager) GarbageCollection(inUseResSet map[string]types.ResourceItem, expireResSet map[string]types.ResourceItem) error {
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
	ipContainerIDMap := make(map[string]string)
	for _, file := range files {
		// skip non checkpoint file
		if ip := net.ParseIP(file.Name()); ip == nil {
			continue
		}

		ipamFile, err := os.Open(filepath.Join(defaultIpamPath, file.Name()))
		if err != nil {
			log.Errorf("failed to open ipam file: %v, %v", file, err)
			continue
		}

		r := bufio.NewReader(ipamFile)
		content, _, err := r.ReadLine()
		if err != nil {
			log.Errorf("Failed to read file %v: %v", file, err)
			err = ipamFile.Close()
			if err != nil {
				log.Errorf("Failed to close file %v: %v", file, err)
			}
			continue
		}
		err = ipamFile.Close()
		if err != nil {
			log.Errorf("Failed to close file %v: %v", file, err)
		}
		ipContainerIDMap[file.Name()] = strings.TrimSpace(string(content))
	}

	for ip, containerID := range ipContainerIDMap {
		if _, ok := sandboxStubSet[containerID]; !ok && containerID != "" {
			log.Warnf("detect ip address leak: %s, removing", ip)
			err := os.Remove(filepath.Join(defaultIpamPath, ip))
			if err != nil {
				log.Errorf("error remove leak ip: %s, err: %v", ip, err)
			}
		}
	}
	return nil
}

func (f *vethResourceManager) GetResourceMapping() (tracing.ResourcePoolStats, error) {
	return nil, nil
}

func (f *vethResourceManager) Stat(context *networkContext, resID string) (types.NetworkResource, error) {
	vethName, _ := link.VethNameForPod(context.pod.Name, context.pod.Namespace, "", defaultPrefix)
	return &types.Veth{
		HostVeth: vethName,
	}, nil
}

func newVPCResourceManager() (ResourceManager, error) {
	mgr := &vethResourceManager{
		runtimeAPI: dockerRuntime{},
	}

	return mgr, nil
}

type containerRuntime interface {
	GetRunningSandbox() ([]string, error)
}

type dockerRuntime struct{}

func (dockerRuntime) GetRunningSandbox() ([]string, error) {
	var containerList []string
	// use env DOCKER_API_VERSION=v1.21 to specify the version to v1.21,
	// otherwise, client can negotiate an appropriate version with server.
	dockerCli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return containerList, fmt.Errorf("error init docker client to restore local lease: %+v", err)
	}
	defer dockerCli.Close()

	timeoutContext, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
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
		timeoutContext, cancel := context.WithTimeout(context.Background(), time.Minute)
		containerInfo, err := dockerCli.ContainerInspect(timeoutContext, container.ID)
		cancel()
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
