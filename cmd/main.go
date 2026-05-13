/*
Copyright 2024 The Kubernetes Authors.

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
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	infrav1 "github.com/openshift/cluster-api-provider-alibaba/api/v1beta1"
	infracontroller "github.com/openshift/cluster-api-provider-alibaba/internal/controller"
	alibabaClient "github.com/openshift/cluster-api-provider-alibaba/pkg/client"
	"github.com/openshift/cluster-api-provider-alibaba/pkg/version"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		healthAddr           string
		concurrency          int
	)

	klog.InitFlags(nil)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&healthAddr, "health-probe-bind-address", ":9440", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of resources to process simultaneously.")
	flag.Parse()

	ctrl.SetLogger(klogr.New())
	setupLog := ctrl.Log.WithName("setup")

	setupLog.Info("Starting cluster-api-provider-alibaba", "version", version.Version.String())

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: healthAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "controller-leader-election-capa",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	if err = (&infracontroller.AlibabaCloudClusterReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("AlibabaCloudCluster"),
		AlibabaCloudClientBuilder: alibabaClient.NewCAPIClient,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: concurrency}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AlibabaCloudCluster")
		os.Exit(1)
	}

	if err = (&infracontroller.AlibabaCloudMachineReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("AlibabaCloudMachine"),
		AlibabaCloudClientBuilder: alibabaClient.NewCAPIClient,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: concurrency}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AlibabaCloudMachine")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
