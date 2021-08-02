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
	goflag "flag"
	"fmt"
	"math/rand"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun"
	"github.com/AliyunContainerService/terway/pkg/apis/crds"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"github.com/AliyunContainerService/terway/pkg/cert"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	_ "github.com/AliyunContainerService/terway/pkg/controller/all"
	"github.com/AliyunContainerService/terway/pkg/controller/vswitch"
	"github.com/AliyunContainerService/terway/pkg/controller/webhook"
	"github.com/AliyunContainerService/terway/pkg/logger"
	"github.com/AliyunContainerService/terway/pkg/utils"
	"github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/pkg/version"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	leaseLockName      string
	leaseLockNamespace string
	healthzBindAddress string

	scheme = runtime.NewScheme()
	log    = ctrl.Log.WithName("setup")
)

func init() {
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	flag.StringVar(&leaseLockName, "lease-lock-name", "terway-controller-lock", "the lease lock resource name")
	flag.StringVar(&leaseLockNamespace, "lease-lock-namespace", "kube-system", "the lease lock resource namespace")
	flag.StringVar(&healthzBindAddress, "healthzBindAddress", "0.0.0.0:80", "for health check")

	flag.String("cluster-id", "", "cluster id for the cluster")
	flag.String("vpc-id", "", "vpc id for the cluster")

	flag.String("cluster-domain", "cluster.local", "domain for the cluster")
	flag.String("controller-namespace", "kube-system", "specific controller run namespace")
	flag.String("controller-name", "terway-controlplane", "specific controller name")
	flag.String("cert-dir", "/var/run/webhook-cert", "webhook cert dir")
	flag.Int("webhook-port", 443, "port for webhook")
	flag.Int("v", 4, "log level")

	_ = viper.BindPFlag("cluster-id", flag.CommandLine.Lookup("cluster-id"))
	_ = viper.BindPFlag("vpc-id", flag.CommandLine.Lookup("vpc-id"))

	_ = viper.BindPFlag("cluster-domain", flag.CommandLine.Lookup("cluster-domain"))
	_ = viper.BindPFlag("controller-namespace", flag.CommandLine.Lookup("controller-namespace"))
	_ = viper.BindPFlag("controller-name", flag.CommandLine.Lookup("controller-name"))
	_ = viper.BindPFlag("cert-dir", flag.CommandLine.Lookup("cert-dir"))
	_ = viper.BindPFlag("webhook-port", flag.CommandLine.Lookup("webhook-port"))

	_ = viper.BindPFlag("podeni-max-concurrent-reconciles", flag.CommandLine.Lookup("podeni-max-concurrent-reconciles"))

	_ = viper.BindPFlag("v", flag.CommandLine.Lookup("v"))

	_ = viper.BindEnv("ALIBABA_CLOUD_REGION_ID")
	_ = viper.BindEnv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	_ = viper.BindEnv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")

	viper.SetDefault("v", 4)
	viper.SetDefault("podeni-max-concurrent-reconciles", 1)

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkv1beta1.AddToScheme(scheme))
}

func main() {
	rand.Seed(time.Now().UnixNano())

	if viper.GetInt("v") > len(logrus.AllLevels) {
		_ = logger.SetLevel(logrus.AllLevels[len(logrus.AllLevels)-1].String())
	} else {
		_ = logger.SetLevel(logrus.AllLevels[viper.GetInt("v")].String())
	}

	ctrl.SetLogger(klogr.New())
	log.Info(fmt.Sprintf("GitCommit %s BuildDate %s Platform %s",
		version.Get().GitCommit, version.Get().BuildDate, version.Get().Platform))

	flag.Parse()

	utils.RegisterClients(ctrl.GetConfigOrDie())

	err := crds.RegisterCRDs()
	if err != nil {
		panic(err)
	}

	err = cert.SyncCert()
	if err != nil {
		panic(err)
	}

	err = utils.ParseClusterConfig()
	if err != nil {
		panic(err)
	}

	log.Info("init cluster config", "clusterID", viper.GetString("cluster-id"), "vpcID", viper.GetString("vpc-id"))

	aliyunClient, err := aliyun.NewAliyun(viper.GetString("ALIBABA_CLOUD_ACCESS_KEY_ID"), viper.GetString("ALIBABA_CLOUD_ACCESS_KEY_SECRET"), viper.GetString("ALIBABA_CLOUD_REGION_ID"), "")
	if err != nil {
		panic(err)
	}

	ctx := ctrl.SetupSignalHandler()
	restConfig := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                     scheme,
		HealthProbeBindAddress:     healthzBindAddress,
		Host:                       "0.0.0.0",
		Port:                       viper.GetInt("webhook-port"),
		CertDir:                    viper.GetString("cert-dir"),
		LeaderElection:             true,
		LeaderElectionID:           viper.GetString("controller-name"),
		LeaderElectionNamespace:    viper.GetString("controller-namespace"),
		LeaderElectionResourceLock: "leases",
		MetricsBindAddress:         "0",
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

	vSwitchCtrl, err := vswitch.NewSwitchPool(aliyunClient)
	if err != nil {
		panic(err)
	}
	err = mgr.Add(vSwitchCtrl)
	if err != nil {
		panic(err)
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
