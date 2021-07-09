package aliyun

import (
	"encoding/hex"
	"math/rand"
	"net"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	eniNamePrefix  = "eni-cni-"
	eniDescription = "interface create by terway"

	eipStatusInUse     = "InUse"
	eipStatusAvailable = "Available"
)

// ENIStatus status for eni
type ENIStatus string

// status for ENIStatus
const (
	ENIStatusInUse     ENIStatus = "InUse"
	ENIStatusAvailable ENIStatus = "Available"
)

// ENIType eni type
type ENIType string

// status for ENIType
const (
	ENITypePrimary   ENIType = "Primary"
	ENITypeSecondary ENIType = "Secondary"
	ENITypeTrunk     ENIType = "Trunk"
	ENITypeMember    ENIType = "Member"
)

var (
	// ENIOpBackoff about 300s backoff
	ENIOpBackoff = wait.Backoff{
		Duration: time.Second * 5,
		Factor:   2,
		Jitter:   0.3,
		Steps:    6,
	}

	// eniStateBackoff about 600s backoff
	eniStateBackoff = wait.Backoff{
		Duration: time.Second * 4,
		Factor:   2,
		Jitter:   0.3,
		Steps:    7,
	}

	// eniReleaseBackoff about 1200s backoff
	eniReleaseBackoff = wait.Backoff{
		Duration: time.Second * 4,
		Factor:   2,
		Jitter:   0.5,
		Steps:    8,
	}
)

func generateEniName() string {
	b := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return eniNamePrefix + hex.EncodeToString(b)
}

func ips2str(ips []net.IP) []string {
	var result []string
	for _, ip := range ips {
		result = append(result, ip.String())
	}
	return result
}

func ipIntersect(a []net.IP, b []net.IP) bool {
	for _, a1 := range a {
		if a1 == nil {
			continue
		}
		for _, b1 := range b {
			if a1.Equal(b1) {
				return true
			}
		}
	}
	return false
}
