/*
Copyright 2022 Terway Authors.

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

package pool

import (
	"time"
)

// Config for eni manager
type Config struct {
	IPv4Enable bool
	IPv6Enable bool

	NodeName string
	// instance metadata
	InstanceID string
	ZoneID     string
	TrunkENIID string

	// eni
	VSwitchIDs       []string
	SecurityGroupIDs []string
	ENITags          map[string]string

	// pool config
	MaxENI  int // max eni manager can allocate
	MaxIdle int
	MinIdle int

	SyncPeriod time.Duration
}
