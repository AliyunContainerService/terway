// Copyright 2019 Authors of Cilium
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

package sysctl

import (
	"bytes"
	"os"
)

func EnsureConf(fPath string, cfg string) error {
	if content, err := os.ReadFile(fPath); err == nil {
		if bytes.Equal(bytes.TrimSpace(content), []byte(cfg)) {
			return nil
		}
	}
	return os.WriteFile(fPath, []byte(cfg), 0644)
}
