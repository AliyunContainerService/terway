//go:build windows
// +build windows

// Copyright 2015 flannel authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package endian

// Taken from a patch by David Anderson who submitted it
// but got rejected by the golang team

import (
	"encoding/binary"
	"unsafe"
)

// nativeEndian is the ByteOrder of the current system.
var nativeEndian binary.ByteOrder

func init() {
	// Examine the memory layout of an int16 to determine system
	// endianness.
	var one int16 = 1
	b := (*byte)(unsafe.Pointer(&one))
	if *b == 0 {
		nativeEndian = binary.BigEndian
	} else {
		nativeEndian = binary.LittleEndian
	}
}

func NativelyLittle() bool {
	return nativeEndian == binary.LittleEndian
}

func GetNativelyByteOrder() binary.ByteOrder {
	return nativeEndian
}
