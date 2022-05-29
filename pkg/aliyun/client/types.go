package client

import (
	"encoding/hex"
	"math/rand"
	"time"

	"github.com/AliyunContainerService/terway/pkg/logger"
)

var log = logger.DefaultLogger.WithField("subSys", "openAPI")

// log fields
const (
	LogFieldAPI              = "api"
	LogFieldRequestID        = "requestID"
	LogFieldInstanceID       = "instanceID"
	LogFieldSecondaryIPCount = "secondaryIPCount"
	LogFieldENIID            = "eni"
	LogFieldEIPID            = "eip"
	LogFieldPrivateIP        = "privateIP"
	LogFieldVSwitchID        = "vSwitchID"
)

const (
	eniNamePrefix     = "eni-cni-"
	eniDescription    = "interface create by terway"
	maxSinglePageSize = 500
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
