package driver

import "net"

type ipvlanDriver struct {
}

func (*ipvlanDriver) Setup(namespace, pod, containerID string, ipv4Addr *net.IPNet, tableID int) error {
	panic("implement me")
}

func (*ipvlanDriver) Teardown(containerID string, ipv4Addr *net.IPNet, tableID int) error {
	panic("implement me")
}
