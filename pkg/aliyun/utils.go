package aliyun

import (
	"encoding/hex"
	"math/rand"
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

	// MetadataAssignPrivateIPBackoff about 10s backoff
	MetadataAssignPrivateIPBackoff = wait.Backoff{
		Duration: time.Millisecond * 1100,
		Factor:   1,
		Jitter:   0.2,
		Steps:    10,
	}

	// MetadataUnAssignPrivateIPBackoff about 10s backoff
	MetadataUnAssignPrivateIPBackoff = wait.Backoff{
		Duration: time.Millisecond * 1100,
		Factor:   1,
		Jitter:   0.2,
		Steps:    10,
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
