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
	"fmt"
	"os"

	"github.com/fsnotify/fsnotify"
	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/pkg/backoff"
)

var (
	cfg         *Config
	viperConfig *viper.Viper
)

func GetConfig() *Config {
	return cfg
}

func SetConfig(c *Config) {
	cfg = c
}

func GetViper() *viper.Viper {
	return viperConfig
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
func ParseAndValidate(configFilePath, credentialFilePath string) (*Config, error) {
	b, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	var c Config
	err = yaml.Unmarshal(b, &c)
	if err != nil {
		return nil, err
	}

	cr, err := ParseAndValidateCredential(credentialFilePath)
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
		c.EnableTrunk = ptr.To(true)
	}
	if c.EnableWebhookInjectResource == nil {
		c.EnableWebhookInjectResource = ptr.To(true)
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

// InitViper initial viper
// only partial config is loaded
func InitViper(configFilePath string, onConfigChange func(e fsnotify.Event)) error {
	viperConfig = viper.New()
	viperConfig.SetConfigFile(configFilePath)
	viperConfig.SetConfigType("yaml")
	viperConfig.WatchConfig()
	viperConfig.OnConfigChange(onConfigChange)

	if err := viperConfig.ReadInConfig(); err != nil {
		return err
	}
	var c Config
	if err := viperConfig.Unmarshal(&c); err != nil {
		return err
	}
	return nil
}

// IsControllerEnabled check if a specified controller enabled or not.
func IsControllerEnabled(name string, enable bool, controllers []string) bool {
	for _, ctrl := range controllers {
		if ctrl == name {
			return true
		}
		if ctrl == "-"+name {
			return false
		}
		if ctrl == "*" {
			return true
		}
	}
	return enable
}
