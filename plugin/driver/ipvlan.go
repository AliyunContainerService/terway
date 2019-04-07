package driver

import "net"

type ipvlanDriver struct {
}

func (*ipvlanDriver) Setup(namespace, pod, containerId string, ipv4Addr *net.IPNet, tableId int) error {
	panic("implement me")
}

func (*ipvlanDriver) Teardown(containerId string, ipv4Addr *net.IPNet, tableId int) error {
	panic("implement me")
}
