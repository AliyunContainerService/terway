/*
Copyright 2025 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ops

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExecutor_GetTimeout(t *testing.T) {
	exec := NewExecutor(nil, nil)

	// ECS ENI
	timeout := exec.GetTimeout("eni-12345")
	assert.Equal(t, 2*time.Minute, timeout)

	// LENI (EFLO)
	timeout = exec.GetTimeout("leni-12345")
	assert.Equal(t, 5*time.Minute, timeout)

	// HDENI (EFLO)
	timeout = exec.GetTimeout("hdeni-12345")
	assert.Equal(t, 5*time.Minute, timeout)
}

func TestExecutor_GetInitialDelay(t *testing.T) {
	exec := NewExecutor(nil, nil)

	// ECS ENI should have smaller initial delay
	ecsDelay := exec.GetInitialDelay("eni-12345")

	// LENI should have larger initial delay
	leniDelay := exec.GetInitialDelay("leni-12345")

	// EFLO should have larger delay than ECS
	assert.Less(t, ecsDelay, leniDelay)
}

func Test_toPtr(t *testing.T) {
	// Empty string should return nil
	result := toPtr("")
	assert.Nil(t, result)

	// Non-empty string should return pointer
	result = toPtr("test")
	assert.NotNil(t, result)
	assert.Equal(t, "test", *result)
}
