//go:build windows
// +build windows

package converters

import (
	"net"

	"github.com/AliyunContainerService/terway/pkg/windows/endian"
)

func Inet_ntoa(ipnr uint32) string {
	ip := net.IPv4(0, 0, 0, 0)
	var bo = endian.GetNativelyByteOrder()
	bo.PutUint32(ip.To4(), ipnr)
	return ip.String()
}

func Inet_aton(ip string) uint32 {
	var bo = endian.GetNativelyByteOrder()
	return bo.Uint32(
		net.ParseIP(ip).To4(),
	)
}
