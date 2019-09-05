package aliyun

import (
	"encoding/hex"
	"k8s.io/apimachinery/pkg/util/wait"
	"math/rand"
	"time"
)

const (
	eniNamePrefix    = "eni-cni-"
	eniDescription   = "interface create by terway"

	eniStatusInUse     = "InUse"
	eniStatusAvailable = "Available"
)

var (
	// eniOpBackoff about 300s backoff
	eniOpBackoff = wait.Backoff{
		Duration: time.Second * 5,
		Factor: 2,
		Jitter: 1,
		Steps: 6,
	}

	// eniStateBackoff about 600s backoff
	eniStateBackoff = wait.Backoff{
		Duration: time.Second * 10,
		Factor: 2,
		Jitter: 1,
		Steps: 6,
	}

	// eniReleaseBackoff about 1200s backoff
	eniReleaseBackoff = wait.Backoff{
		Duration: time.Second * 5,
		Factor: 2,
		Jitter: 1,
		Steps: 8,
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
