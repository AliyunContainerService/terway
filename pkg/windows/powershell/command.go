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

package powershell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// commandWrapper ensures that exceptions are written to stdout and the powershell process exit code is -1
const commandWrapper = `$ErrorActionPreference="Stop";try { %s } catch { Write-Host $_; os.Exit(-1) }`

// RunCommand executes a given powershell command.
func RunCommand(command string) ([]byte, error) {
	var s = fmt.Sprintf(commandWrapper, command)
	var cmd = exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", s)
	var stdout, err = cmd.Output()
	if err != nil {
		if cmd.ProcessState.ExitCode() != 0 {
			var message = strings.TrimSpace(string(stdout))
			return nil, errors.New(message)
		}
		return nil, err
	}
	return stdout, nil
}

// RunCommandWithJsonResult executes a given powershell command.
func RunCommandWithJsonResult(command string, v interface{}) error {
	var s = fmt.Sprintf(commandWrapper, "ConvertTo-Json (%s)")
	s = fmt.Sprintf(s, command)
	var stdout, err = RunCommand(s)
	if err != nil {
		return err
	}
	err = json.Unmarshal(stdout, v)
	if err != nil {
		return err
	}
	return nil
}
