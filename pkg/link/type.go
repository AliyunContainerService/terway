package link

import (
	"errors"
)

var (
	ErrUnsupported = errors.New("not supported arch")
	ErrNotFound    = errors.New("not found")
)
