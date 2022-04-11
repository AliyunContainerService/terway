package utils

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestCNIUtils(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "cni utils")
}
