package daemon

import (
	"math/rand"
	"time"
)

const (
	randomStringLength = 5
	randomCharset      = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// randomString generates a string with lower case letters & numbers
func randomString() string {
	// always use a new rand generator ?
	rand.Seed(time.Now().UnixNano())

	bytes := make([]byte, randomStringLength)

	for i := range bytes {
		bytes[i] = randomCharset[rand.Int63()%int64(len(randomCharset))]
	}

	return string(bytes)
}
