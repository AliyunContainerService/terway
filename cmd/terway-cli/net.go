package main

import (
	"errors"
	"net"
)

var (
	netInterfaces []net.Interface
)

func getNetInterfaces() ([]net.Interface, error) {
	if netInterfaces != nil {
		return netInterfaces, nil
	}

	var err error
	netInterfaces, err = net.Interfaces()

	return netInterfaces, err
}

func getInterfaceByMAC(mac string) (net.Interface, error) {
	interfaces, err := getNetInterfaces()
	if err != nil {
		return net.Interface{}, err
	}

	for _, i := range interfaces {
		if i.HardwareAddr.String() == mac {
			return i, nil
		}
	}

	return net.Interface{}, errors.New("not found")
}
