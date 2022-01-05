package link

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// VethNameForPod return host-side veth name for pod
// max veth length is 15
func VethNameForPod(name, namespace, ifName, prefix string) (string, error) {
	// A SHA1 is always 20 bytes long, and so is sufficient for generating the
	// veth name and mac addr.
	h := sha1.New()
	if ifName == "eth0" {
		ifName = ""
	}
	_, err := h.Write([]byte(namespace + "." + name + ifName))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", prefix, hex.EncodeToString(h.Sum(nil))[:11]), nil
}
