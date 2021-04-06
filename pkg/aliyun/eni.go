package aliyun

import (
	"fmt"
	"net"

	errors2 "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/link"
	"github.com/AliyunContainerService/terway/types"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const maxSinglePageSize = 100

// ENIInfoGetter interface to get eni information
type ENIInfoGetter interface {
	GetENIConfigByMac(mac string) (*types.ENI, error)
	GetENIConfigByID(eniID string) (*types.ENI, error)
	GetENIPrivateAddresses(eniID string) ([]net.IP, error)
	GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error)
	GetSecondaryENIMACs() ([]string, error)
}

type eniMetadata struct {
	ignoreLinkNotExist bool
}

func (e *eniMetadata) GetENIConfigByMac(mac string) (*types.ENI, error) {
	var (
		eni types.ENI
		err error
	)

	eni.ID, err = metadata.GetENIID(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni id from metaserver, mac: %s", mac)
	}
	eni.MAC = mac

	ip, err := metadata.GetENIPrimaryIP(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni address from metaserver, mac: %s", mac)
	}
	netmask, err := metadata.GetENINetMask(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni netmask from metaserver, mac: %s", mac)
	}

	mask := net.ParseIP(netmask)
	if mask == nil {
		return nil, errors.Errorf("error parse eni mask: %s from metadata", netmask)
	}
	eni.Address = net.IPNet{
		IP: ip,
		// fixme: dual stack support
		Mask: net.IPv4Mask(mask[12], mask[13], mask[14], mask[15]),
	}
	gw, err := metadata.GetENIGateway(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni gateway from metaserver, mac: %s", mac)
	}
	eni.Gateway = gw

	vswitch, err := metadata.GetENIVSwitch(mac)
	if err != nil {
		return nil, errors.Wrapf(err, "error get eni vswitch from metaserver, mac: %s", mac)
	}
	eni.VSwitch = vswitch

	eni.Name, err = link.GetDeviceName(mac)
	if err != nil {
		if !e.ignoreLinkNotExist {
			return nil, errors.Wrapf(err, "error get device name for eni: %s", mac)
		}
		logrus.Warnf("error get device name for eni: %v", err)
	}

	eni.DeviceNumber, err = link.GetDeviceNumber(mac)
	if err != nil {
		if !e.ignoreLinkNotExist {
			return nil, errors.Wrapf(err, "error get device number for eni: %s", mac)
		}
		logrus.Warnf("error get device number for eni: %s", mac)
	}

	return &eni, nil
}

func (e *eniMetadata) GetENIConfigByID(eniID string) (*types.ENI, error) {
	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		id, err := metadata.GetENIID(mac)
		if err != nil {
			return nil, errors.Wrapf(err, "error get eni id for mac: %s from metadata", mac)
		}
		if eniID == id {
			return e.GetENIConfigByMac(mac)
		}
	}
	return nil, errors.Wrapf(errors2.ErrNotFound, fmt.Sprintf("eni id: %s", eniID))
}

func (e *eniMetadata) GetENIPrivateAddresses(eniID string) ([]net.IP, error) {
	var addressList []net.IP
	eni, err := e.GetENIConfigByID(eniID)
	if err != nil {
		return addressList, err
	}

	return metadata.GetENIPrivateIPs(eni.MAC)
}

func (e *eniMetadata) getAttachMACList() ([]string, error) {
	macs, err := metadata.GetENIsMAC()
	return macs, errors.Wrapf(err, "error get eni list from metadata")
}

func (e *eniMetadata) GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error) {
	var enis []*types.ENI

	mainENIMac, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		return enis, errors.Wrapf(err, "error get main eni form metadata")
	}

	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		if !containsMainENI {
			if mac == mainENIMac {
				continue
			}
		}
		eni, err := e.GetENIConfigByMac(mac)
		if err != nil {
			return nil, errors.Wrapf(err, "error get eni info for mac: %s from metadata", mac)
		}
		enis = append(enis, eni)
	}
	return enis, nil
}

// GetSecondaryENIMACs return secondary ENI macs
func (e *eniMetadata) GetSecondaryENIMACs() ([]string, error) {
	var result []string
	mainENIMac, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		return nil, errors.Wrapf(err, "error get main eni form metadata")
	}
	macs, err := e.getAttachMACList()
	if err != nil {
		return nil, err
	}
	for _, mac := range macs {
		if mac == mainENIMac {
			continue
		}
		result = append(result, mac)
	}
	return result, nil
}

type eniOpenAPI struct {
	clientSet *ClientMgr
}

// GetAttachedENIs GetAttachedENIs
func (e *eniOpenAPI) GetAttachedENIs(instanceID string, containsMainENI bool) ([]*types.ENI, error) {
	return nil, fmt.Errorf("unimpled")
}

func (e *eniOpenAPI) GetENIPrivateAddresses(eniID string) ([]net.IP, error) {
	return nil, fmt.Errorf("unimpled")
}

func (*eniOpenAPI) GetENIConfigByMac(mac string) (*types.ENI, error) {
	panic("implement me")
}

func (*eniOpenAPI) GetENIConfigByID(eniID string) (*types.ENI, error) {
	panic("implement me")
}

// GetSecondaryENIMACs return secondary ENI macs
func (e *eniOpenAPI) GetSecondaryENIMACs() ([]string, error) {
	return nil, fmt.Errorf("unimpled")
}
