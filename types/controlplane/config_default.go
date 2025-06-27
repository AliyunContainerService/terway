//go:build default_build

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
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/types/secret"
)

type Config struct {
	// controller config
	LeaseLockName      string `json:"leaseLockName" validate:"required" mod:"default=terway-controller-lock" yaml:"leaseLockName"`
	LeaseLockNamespace string `json:"leaseLockNamespace" validate:"required" mod:"default=kube-system" yaml:"leaseLockNamespace"`
	LeaseDuration      string `json:"leaseDuration" yaml:"leaseDuration"`
	RenewDeadline      string `json:"renewDeadline" yaml:"renewDeadline"`
	RetryPeriod        string `json:"retryPeriod" yaml:"retryPeriod"`

	ControllerNamespace string `json:"controllerNamespace" validate:"required" mod:"default=kube-system" yaml:"controllerNamespace"`
	ControllerName      string `json:"controllerName" validate:"required" mod:"default=terway-controlplane" yaml:"controllerName"`

	HealthzBindAddress string `json:"healthzBindAddress" validate:"required,tcp_addr" mod:"default=0.0.0.0:80" yaml:"healthzBindAddress"`
	MetricsBindAddress string `json:"metricsBindAddress" validate:"required" mod:"default=0" yaml:"metricsBindAddress"`
	ClusterDomain      string `json:"clusterDomain" validate:"required" mod:"default=cluster.local" yaml:"clusterDomain"`
	DisableWebhook     bool   `json:"disableWebhook" yaml:"disableWebhook"`
	WebhookPort        int    `json:"webhookPort" validate:"gt=0,lte=65535" mod:"default=4443" yaml:"webhookPort"`
	CertDir            string `json:"certDir" validate:"required" mod:"default=/var/run/webhook-cert" yaml:"certDir"`
	LeaderElection     bool   `json:"leaderElection" yaml:"leaderElection"`
	EnableTrace        bool   `json:"enableTrace" yaml:"enableTrace"`

	PodMaxConcurrent    int `json:"podMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=10" yaml:"podMaxConcurrent"`
	PodENIMaxConcurrent int `json:"podENIMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=10" yaml:"podENIMaxConcurrent"`
	NodeController
	MultiIPController
	ENIController

	Controllers []string `json:"controllers" yaml:"controllers"`

	// cluster info for controlplane
	RegionID  string `json:"regionID" validate:"required" yaml:"regionID"`
	ClusterID string `json:"clusterID" validate:"required" yaml:"clusterID"`
	VPCID     string `json:"vpcID" validate:"required" yaml:"vpcID"`

	EnableTrunk        *bool  `json:"enableTrunk,omitempty" yaml:"enableTrunk,omitempty"`
	EnableDevicePlugin bool   `json:"enableDevicePlugin" yaml:"enableDevicePlugin"`
	IPStack            string `json:"ipStack,omitempty" validate:"oneof=ipv4 ipv6 dual" mod:"default=ipv4" yaml:"ipStack,omitempty"`

	EnableWebhookInjectResource *bool `json:"enableWebhookInjectResource,omitempty" yaml:"enableWebhookInjectResource,omitempty"`

	KubeClientQPS   float32 `json:"kubeClientQPS" validate:"gt=0,lte=10000" mod:"default=20" yaml:"kubeClientQPS"`
	KubeClientBurst int     `json:"kubeClientBurst" validate:"gt=0,lte=10000" mod:"default=30" yaml:"kubeClientBurst"`

	VSwitchPoolSize int    `json:"vSwitchPoolSize" validate:"gt=0" mod:"default=1000" yaml:"vSwitchPoolSize"`
	VSwitchCacheTTL string `json:"vSwitchCacheTTL" mod:"default=20m0s" yaml:"vSwitchCacheTTL"`

	CustomStatefulWorkloadKinds []string `json:"customStatefulWorkloadKinds" yaml:"customStatefulWorkloadKinds"`

	BackoffOverride map[string]backoff.ExtendedBackoff `json:"backoffOverride,omitempty" yaml:"backoffOverride,omitempty"`
	IPAMType        string                             `json:"ipamType" yaml:"ipamType"`
	CentralizedIPAM bool                               `json:"centralizedIPAM,omitempty" yaml:"centralizedIPAM,omitempty"`

	RateLimit map[string]int `json:"rateLimit" yaml:"rateLimit"`

	Degradation Degradation `json:"degradation" yaml:"degradation"`

	Credential
}

type Credential struct {
	AccessKey      secret.Secret `json:"accessKey" validate:"required_with=AccessSecret" yaml:"accessKey"`
	AccessSecret   secret.Secret `json:"accessSecret" validate:"required_with=AccessKey" yaml:"accessSecret"`
	CredentialPath string        `json:"credentialPath" yaml:"credentialPath"`
	OtelEndpoint   string        `json:"otelEndpoint" yaml:"otelEndpoint"`
	OtelToken      secret.Secret `json:"otelToken" yaml:"otelToken"`
}

type MultiIPController struct {
	MultiIPPodMaxConcurrent       int    `json:"multiIPPodMaxConcurrent" validate:"gt=0,lte=20000" mod:"default=500" yaml:"multiIPPodMaxConcurrent"`
	MultiIPNodeMaxConcurrent      int    `json:"multiIPNodeMaxConcurrent" validate:"gt=0,lte=20000" mod:"default=500" yaml:"multiIPNodeMaxConcurrent"`
	MultiIPNodeSyncPeriod         string `json:"multiIPNodeSyncPeriod" mod:"default=12h" yaml:"multiIPNodeSyncPeriod"`
	MultiIPGCPeriod               string `json:"multiIPGCPeriod" mod:"default=2m" yaml:"multiIPGCPeriod"`
	MultiIPMinSyncPeriodOnFailure string `json:"multiIPMinSyncPeriodOnFailure" mod:"default=1s" yaml:"multiIPMinSyncPeriodOnFailure"`
	MultiIPMaxSyncPeriodOnFailure string `json:"multiIPMaxSyncPeriodOnFailure" mod:"default=300s" yaml:"multiIPMaxSyncPeriodOnFailure"`
}

type NodeController struct {
	NodeMaxConcurrent  int               `json:"nodeMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=10" yaml:"nodeMaxConcurrent"`
	NodeLabelWhiteList map[string]string `json:"nodeLabelWhiteList" yaml:"nodeLabelWhiteList"`
}

type ENIController struct {
	ENIMaxConcurrent int `json:"eniMaxConcurrent" validate:"gt=0,lte=10000" mod:"default=300" yaml:"eniMaxConcurrent"`
}

type Degradation string

const (
	DegradationL0 Degradation = "l0"
	DegradationL1 Degradation = "l1"
)
