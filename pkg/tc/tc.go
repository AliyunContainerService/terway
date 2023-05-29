package tc

import (
	"fmt"
	"math"

	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

// TrafficShapingRule the interface traffic shaping rule
type TrafficShapingRule struct {
	// rate in bytes
	Rate uint64
}

func burst(rate uint64, mtu int) uint32 {
	return uint32(math.Ceil(math.Max(float64(rate)/milliSeconds, float64(mtu))))
}

func time2Tick(time uint32) uint32 {
	return uint32(float64(time) * float64(netlink.TickInUsec()))
}

func buffer(rate uint64, burst uint32) uint32 {
	return time2Tick(uint32(float64(burst) * float64(netlink.TIME_UNITS_PER_SEC) / float64(rate)))
}

func limit(rate uint64, latency float64, buffer uint32) uint32 {
	return uint32(float64(rate)*latency/float64(netlink.TIME_UNITS_PER_SEC)) + buffer
}

func latencyInUsec(latencyInMillis float64) float64 {
	return float64(netlink.TIME_UNITS_PER_SEC) * (latencyInMillis / 1000.0)
}

const latencyInMillis = 25
const hardwareHeaderLen = 1500
const milliSeconds = 1000

// SetRule set the traffic rule on interface
func SetRule(dev netlink.Link, rule *TrafficShapingRule) error {
	if rule.Rate <= 0 {
		return fmt.Errorf("invalid rate %d", rule.Rate)
	}

	burst := burst(rule.Rate, dev.Attrs().MTU+hardwareHeaderLen)
	buffer := buffer(rule.Rate, burst)
	latency := latencyInUsec(latencyInMillis)
	limit := limit(rule.Rate, latency, burst)
	//log.Infof("set tc qdics add dev %v/%s root tbf rate %d burst %d", dev.Attrs().Namespace, dev.Attrs().Name, rule.Rate, burst)

	tbf := &netlink.Tbf{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: dev.Attrs().Index,
			Handle:    netlink.MakeHandle(1, 0),
			Parent:    netlink.HANDLE_ROOT,
		},
		Rate:     rule.Rate,
		Limit:    uint32(limit),
		Buffer:   uint32(buffer),
		Minburst: uint32(dev.Attrs().MTU),
	}

	if err := netlink.QdiscReplace(tbf); err != nil {
		return errors.Wrapf(err, "can not replace qdics %+v on device %v/%s", tbf, dev.Attrs().Namespace, dev.Attrs().Name)
	}

	return nil
}
