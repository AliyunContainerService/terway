//go:build windows
// +build windows

package converters

import (
	"bytes"
	"unsafe"
)

func UnsafeBytesToString(bs []byte) string {
	if len(bs) == 0 {
		return ""
	}
	return *(*string)(unsafe.Pointer(&bs))
}

func UnsafeUTF16BytesToString(bs []byte) string {
	if len(bs) == 0 {
		return ""
	}
	return UnsafeBytesToString(bytes.Trim(bs, "\x00"))
}
