package aliyun

import (
	"encoding/hex"
	"math/rand"
	"time"
)

const (
	eniNamePrefix    = "eni-cni-"
	eniDescription   = "interface create by terway"
	eniCreateTimeout = 30
	eniBindTimeout   = 60

	eniStatusInUse     = "InUse"
	eniStatusAvailable = "Available"
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
