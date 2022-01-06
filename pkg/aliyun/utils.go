package aliyun

import (
	"encoding/hex"
	"math/rand"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

type Client interface {
	ECS() *ecs.Client
	VPC() *vpc.Client
}

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

func generateEniName() string {
	b := make([]byte, 3)
	rand.Seed(time.Now().UnixNano())
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return eniNamePrefix + hex.EncodeToString(b)
}
