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
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	cpv1 "github.com/SammZhu/openshift-capi-alicloud/api/controlplane/v1beta1"
	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
	infracontroller "github.com/SammZhu/openshift-capi-alicloud/internal/controller"
	infrawebhook "github.com/SammZhu/openshift-capi-alicloud/internal/webhook/v1beta1"
	alibabaClient "github.com/SammZhu/openshift-capi-alicloud/pkg/client"
	"github.com/SammZhu/openshift-capi-alicloud/pkg/version"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))
	utilruntime.Must(cpv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		healthAddr           string
		concurrency          int
		enableWebhooks       bool
	)

	klog.InitFlags(nil)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&healthAddr, "health-probe-bind-address", ":9440", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of resources to process simultaneously.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", true, "Enable the admission webhooks. Requires serving certs mounted at the webhook server cert dir; disable for local runs without certs.")
	flag.Parse()

	ctrl.SetLogger(klogr.New())
	setupLog := ctrl.Log.WithName("setup")

	setupLog.Info("Starting cluster-api-provider-alibaba", "version", version.Version.String())
	setupLog.Info("Resolved Alibaba Cloud credential mode", "source", alibabaClient.CredentialSource())

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: healthAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "controller-leader-election-capa",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	// Preflight: this is a CAPI infrastructure provider — it cannot function
	// without the Cluster API core CRDs (cluster.x-k8s.io). Fail fast with a clear
	// message instead of a cryptic watch/cache error if CAPI core is not installed.
	if _, err := mgr.GetRESTMapper().RESTMapping(
		schema.GroupKind{Group: clusterv1.GroupVersion.Group, Kind: "Machine"},
	); err != nil {
		setupLog.Error(err, "Cluster API core CRDs (cluster.x-k8s.io) not found — "+
			"install cluster-api (clusterctl init / the CAPI operator) before this provider")
		os.Exit(1)
	}

	// Preflight: refuse to run alongside two Cluster API cores. A self-bundled core
	// (capi-system) and the OCP-hosted core (cluster-capi-operator,
	// openshift-cluster-api) are mutually exclusive — they fight over the shared
	// cluster.x-k8s.io CRDs/webhooks/leader election. Use the API reader (the
	// manager cache is not started yet). Single core (bundled or reused) is fine
	// (P3-CAPA.29 / #79).
	if namespaces, err := infracontroller.DetectCAPICoreNamespaces(ctx, mgr.GetAPIReader()); err != nil {
		setupLog.Info("CAPI core coexistence preflight: unable to list Deployments, skipping", "error", err.Error())
	} else if infracontroller.ClassifyCAPICore(namespaces) == infracontroller.CAPICoreConflict {
		setupLog.Error(nil, "Multiple Cluster API cores detected — a self-bundled core and the "+
			"OCP-hosted core (cluster-capi-operator) cannot coexist; they fight over the cluster.x-k8s.io "+
			"CRDs/webhooks/leader election. Deploy provider-only against the OCP-hosted core, or remove it "+
			"and keep the self-bundled core, then restart.", "namespaces", namespaces)
		os.Exit(1)
	}

	if err = (&infracontroller.AlibabaCloudClusterReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("AlibabaCloudCluster"),
		Recorder:                  mgr.GetEventRecorderFor("alibabacloudcluster-controller"),
		AlibabaCloudClientBuilder: alibabaClient.NewCAPIClient,
		CCMGracePeriod:            infracontroller.DefaultCCMGracePeriod,
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

	if err = (&infracontroller.CertificateSigningRequestReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("CSR"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CSR")
		os.Exit(1)
	}

	if err = (&infracontroller.AlibabaCloudControlPlaneReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("AlibabaCloudControlPlane"),
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: concurrency}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AlibabaCloudControlPlane")
		os.Exit(1)
	}

	if err = (&infracontroller.AlibabaCloudMachineTemplateReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		Log:                       ctrl.Log.WithName("controllers").WithName("AlibabaCloudMachineTemplate"),
		AlibabaCloudClientBuilder: alibabaClient.NewCAPIClient,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: concurrency}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AlibabaCloudMachineTemplate")
		os.Exit(1)
	}

	if enableWebhooks {
		if err = (&infrawebhook.AlibabaCloudMachineWebhook{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "AlibabaCloudMachine")
			os.Exit(1)
		}
		if err = (&infrawebhook.AlibabaCloudClusterWebhook{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "AlibabaCloudCluster")
			os.Exit(1)
		}
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
