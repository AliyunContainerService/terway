package link

import (
	"errors"
)

// define err
var (
	ErrUnsupported = errors.New("not supported arch")
	ErrNotFound    = errors.New("not found")
)
