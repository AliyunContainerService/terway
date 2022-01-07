/*
Copyright 2021 Terway Authors.

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

package controlplane

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/backoff"

	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"k8s.io/apimachinery/pkg/util/yaml"
)

var (
	config     string
	credential string
	cfg        *Config
	once       sync.Once
)

func init() {
	flag.StringVar(&config, "config", "/etc/config/ctrl-config.yaml", "config file for controlplane")
	flag.StringVar(&credential, "credential", "/etc/credential/ctrl-secret.yaml", "secret file for controlplane")
}

func GetConfig() *Config {
	once.Do(func() {
		var err error
		cfg, err = ParseAndValidate()
		if err != nil {
			panic(err)
		}
	})
	return cfg
}

func ParseAndValidateCredential(file string) (*Credential, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var c Credential
	err = yaml.Unmarshal(b, &c)
	if err != nil {
		return nil, err
	}

	err = validator.New().Struct(&c)
	return &c, err
}

// ParseAndValidate ready config and verify it
func ParseAndValidate() (*Config, error) {
	b, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}

	var c Config
	err = yaml.Unmarshal(b, &c)
	if err != nil {
		return nil, err
	}

	cr, err := ParseAndValidateCredential(credential)
	if err != nil {
		return nil, err
	}
	c.Credential = *cr

	mod := modifiers.New()
	err = mod.Struct(context.Background(), &c)
	if err != nil {
		return nil, err
	}

	if c.EnableTrunk == nil {
		t := true
		c.EnableTrunk = &t
	}

	if c.RegionID == "" {
		c.RegionID, err = metadata.GetLocalRegion()
		if err != nil || c.RegionID == "" {
			return nil, fmt.Errorf("error get region from metadata %v", err)
		}
	}

	err = validator.New().Struct(&c)
	if err != nil {
		return nil, err
	}

	backoff.OverrideBackoff(c.BackoffOverride)

	cfg = &c

	return &c, nil
}
