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

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"github.com/AliyunContainerService/terway/pkg/apis/crds"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/cert"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	_ "github.com/AliyunContainerService/terway/pkg/controller/all"
	"github.com/AliyunContainerService/terway/pkg/controller/endpoint"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/controller/webhook"
	terwayMetric "github.com/AliyunContainerService/terway/pkg/metric"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/AliyunContainerService/terway/types/controlplane"
	"k8s.io/client-go/util/flowcontrol"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/pkg/version"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	scheme = runtime.NewScheme()
	log    = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkv1beta1.AddToScheme(scheme))

	metrics.Registry.MustRegister(terwayMetric.OpenAPILatency)
}

func main() {
	rand.Seed(time.Now().UnixNano())
	flag.Parse()

	ctrl.SetLogger(klogr.New())
	log.Info(fmt.Sprintf("GitCommit %s BuildDate %s Platform %s",
		version.Get().GitCommit, version.Get().BuildDate, version.Get().Platform))

	cfg := controlplane.GetConfig()
	log.Info("using config", "config", cfg)

	utils.SetStsKinds(cfg.CustomStatefulWorkloadKinds)

	utils.RegisterClients(ctrl.GetConfigOrDie())

	err := crds.RegisterCRDs()
	if err != nil {
		panic(err)
	}

	err = cert.SyncCert(cfg.ControllerNamespace, cfg.ControllerName, cfg.ClusterDomain, cfg.CertDir)
	if err != nil {
		panic(err)
	}

	clientSet, err := credential.NewClientSet(string(cfg.Credential.AccessKey), string(cfg.Credential.AccessSecret), cfg.RegionID, cfg.CredentialPath, cfg.SecretNamespace, cfg.SecretName)
	if err != nil {
		panic(err)
	}

	aliyunClient, err := client.New(clientSet, flowcontrol.NewTokenBucketRateLimiter(cfg.ReadOnlyQPS, cfg.ReadOnlyBurst), flowcontrol.NewTokenBucketRateLimiter(cfg.MutatingQPS, cfg.MutatingBurst))
	if err != nil {
		panic(err)
	}

	ctx := ctrl.SetupSignalHandler()
	restConfig := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                     scheme,
		HealthProbeBindAddress:     cfg.HealthzBindAddress,
		Host:                       "0.0.0.0",
		Port:                       cfg.WebhookPort,
		CertDir:                    cfg.CertDir,
		LeaderElection:             cfg.LeaderElection,
		LeaderElectionID:           cfg.ControllerName,
		LeaderElectionNamespace:    cfg.ControllerNamespace,
		LeaderElectionResourceLock: "leases",
		MetricsBindAddress:         cfg.MetricsBindAddress,
	})
	if err != nil {
		panic(err)
	}

	err = mgr.AddHealthzCheck("healthz", healthz.Ping)
	if err != nil {
		panic(err)
	}
	err = mgr.AddReadyzCheck("readyz", healthz.Ping)
	if err != nil {
		panic(err)
	}

	mgr.GetWebhookServer().Register("/mutating", webhook.MutatingHook(mgr.GetClient()))
	mgr.GetWebhookServer().Register("/validate", webhook.ValidateHook())

	vSwitchCtrl, err := vswitch.NewSwitchPool(cfg.VSwitchPoolSize, cfg.VSwitchCacheTTL)
	if err != nil {
		panic(err)
	}

	if cfg.RegisterEndpoint {
		ipStr := os.Getenv("MY_POD_IP")
		if ipStr == "" {
			panic("podIP is not found")
		}
		// if enable Service name should equal cfg.ControllerName
		ep := &endpoint.Endpoint{PodIP: ipStr, Namespace: cfg.ControllerNamespace, Name: cfg.ControllerName}
		err = mgr.Add(ep)
		if err != nil {
			panic(err)
		}
	}

	for name := range register.Controllers {
		err = register.Controllers[name](mgr, aliyunClient, vSwitchCtrl)
		if err != nil {
			panic(err)
		}
		log.Info("register controller", "controller", name)
	}

	log.Info("controller started")
	err = mgr.Start(ctx)
	if err != nil {
		panic(err)
	}
}
