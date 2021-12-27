//go:build default_build
// +build default_build

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
	"github.com/AliyunContainerService/terway/types/secret"
)

type Config struct {
	// controller config
	LeaseLockName       string `json:"leaseLockName" validate:"required" mod:"default=terway-controller-lock"`
	LeaseLockNamespace  string `json:"leaseLockNamespace" validate:"required" mod:"default=kube-system"`
	ControllerNamespace string `json:"controllerNamespace" validate:"required" mod:"default=kube-system"`
	ControllerName      string `json:"controllerName" validate:"required" mod:"default=terway-controlplane"`

	HealthzBindAddress string `json:"healthzBindAddress" validate:"required,tcp_addr" mod:"default=0.0.0.0:80"`
	MetricsBindAddress string `json:"metricsBindAddress" validate:"required" mod:"default=0"`
	ClusterDomain      string `json:"clusterDomain" validate:"required,fqdn" mod:"default=cluster.local"`
	WebhookPort        int    `json:"webhookPort" validate:"gt=0,lte=65535" mod:"default=4443"`
	CertDir            string `json:"certDir" validate:"required" mod:"default=/var/run/webhook-cert"`
	LeaderElection     bool   `json:"leaderElection"`
	RegisterEndpoint   bool   `json:"registerEndpoint"`

	PodMaxConcurrent    int `json:"podMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=10"`
	PodENIMaxConcurrent int `json:"podENIMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=10"`

	// cluster info for controlplane
	RegionID  string `json:"regionID" validate:"required"`
	ClusterID string `json:"clusterID" validate:"required"`
	VPCID     string `json:"vpcID" validate:"required"`

	EnableTrunk *bool  `json:"enableTrunk,omitempty"`
	IPStack     string `json:"ipStack,omitempty" validate:"oneof=ipv4 ipv6 dual" mod:"default=ipv4"`

	ReadOnlyQPS   int `json:"readOnlyQPS" validate:"gt=0,lte=10000" mod:"default=8"`
	ReadOnlyBurst int `json:"readOnlyBurst" validate:"gt=0,lte=10000" mod:"default=10"`
	MutatingQPS   int `json:"mutatingQPS" validate:"gt=0,lte=10000" mod:"default=4"`
	MutatingBurst int `json:"mutatingBurst" validate:"gt=0,lte=10000" mod:"default=5"`

	VSwitchPoolSize int    `json:"vSwitchPoolSize" validate:"gt=0" mod:"default=1000"`
	VSwitchCacheTTL string `json:"vSwitchCacheTTL" mod:"default=20m0s"`

	Credential
}

type Credential struct {
	AccessKey       secret.Secret `json:"accessKey" validate:"required_with=AccessSecret"`
	AccessSecret    secret.Secret `json:"accessSecret" validate:"required_with=AccessKey"`
	CredentialPath  string        `json:"credentialPath"`
	SecretNamespace string        `json:"secretNamespace" validate:"required_with=SecretName"`
	SecretName      string        `json:"secretName" validate:"required_with=SecretNamespace"`
}
