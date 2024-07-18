//go:build !linux

package daemon

import (
	"k8s.io/apimachinery/pkg/util/sets"
)

func gcLeakedRules(existIP sets.Set[string]) {}
