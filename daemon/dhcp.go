package daemon

import (
	"fmt"
	"github.com/d2g/dhcp4"
	"github.com/d2g/dhcp4client"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"math/rand"
	"net"
	"time"
)

func newDHCPClient(link netlink.Link) (*dhcp4client.Client, error) {
	pktsock, err := dhcp4client.NewPacketSock(link.Attrs().Index)
	if err != nil {
		return nil, err
	}

	return dhcp4client.New(
		dhcp4client.HardwareAddr(link.Attrs().HardwareAddr),
		dhcp4client.Timeout(5*time.Second),
		dhcp4client.Broadcast(false),
		dhcp4client.Connection(pktsock),
	)
}

// RFC 2131 suggests using exponential backoff, starting with 4sec
// and randomized to +/- 1sec
const resendDelay0 = 4 * time.Second
const resendDelayMax = 32 * time.Second
const resendCount = 3

// jitter returns a random value within [-span, span) range
func jitter(span time.Duration) time.Duration {
	return time.Duration(float64(span) * (2.0*rand.Float64() - 1.0))
}

func backoffRetry(f func() (*dhcp4.Packet, error)) (*dhcp4.Packet, error) {
	var baseDelay time.Duration = resendDelay0

	for i := 0; i < resendCount; i++ {
		pkt, err := f()
		if err == nil {
			return pkt, nil
		}

		logrus.Print(err)

		time.Sleep(baseDelay + jitter(time.Second))

		if baseDelay < resendDelayMax {
			baseDelay *= 2
		}
	}

	return nil, fmt.Errorf("retry time exceed")
}

func parseGateway(opts dhcp4.Options) net.IP {
	// StaticRoutes format: pairs of:
	// Dest = 4 bytes; Classful IP subnet
	// Router = 4 bytes; IP address of router

	logrus.Debugf("options %++v", opts)
	if opts, ok := opts[dhcp4.OptionRouter]; ok {
		if len(opts) == 4 {
			return net.IP(opts)
		}
	}

	//if opt, ok := opts[dhcp4.OptionStaticRoute]; ok {
	//	for len(opt) >= 8 {
	//		sn := opt[0:4]
	//		r := opt[4:8]
	//		dstIP := net.IP(sn)
	//		dstCIDR := net.IPNet{
	//			IP: dstIP,
	//			Mask: dstIP.DefaultMask(),
	//		}
	//		gateway := net.IP(r)
	//
	//		rt := &network.StaticRoute{
	//			Destination: dstCIDR.String(),
	//			NextHop: gateway.String(),
	//		}
	//		routes = append(routes, rt)
	//		opt = opt[8:]
	//	}
	//}

	return net.IP{}
}

func parseSubnetMask(opts dhcp4.Options) net.IPMask {
	mask, ok := opts[dhcp4.OptionSubnetMask]
	if !ok {
		return nil
	}

	return net.IPMask(mask)
}
