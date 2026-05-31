// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/operator/controller"
	"github.com/nstance-dev/nstance/internal/operator/leader"
	"github.com/nstance-dev/nstance/internal/operator/webhooks"
)

var (
	apiScheme = runtime.NewScheme()
	setupLog  = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(apiScheme))
	utilruntime.Must(infrastructurev1beta1.AddToScheme(apiScheme))
	utilruntime.Must(clusterv1.AddToScheme(apiScheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var configPath string
	var disableWebhooks bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&configPath, "config", "/etc/nstance/operator/config.yaml", "Path to the operator configuration file.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&disableWebhooks, "disable-webhooks", false,
		"Disable admission webhooks (useful for development without certs).")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	restConfig := ctrl.GetConfigOrDie()
	if os.Getenv("NSTANCE_K8S_JSON") == "true" {
		restConfig.ContentType = "application/json"
	}

	// verify CAPI and Nstance CRDs are installed
	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "unable to create discovery client")
		os.Exit(1)
	}
	requiredResources := map[schema.GroupVersion][]string{
		clusterv1.GroupVersion:             {"clusters", "machinepools"},
		infrastructurev1beta1.GroupVersion: {"nstanceclusters", "nstancemachinepools", "nstancemachines", "nstanceshardgroups"},
	}
	for gv, resources := range requiredResources {
		apiResources, err := dc.ServerResourcesForGroupVersion(gv.String())
		if err != nil {
			setupLog.Error(err, "required CRDs are not installed", "groupVersion", gv.String())
			os.Exit(1)
		}
		registered := make(map[string]bool)
		for _, r := range apiResources.APIResources {
			registered[r.Name] = true
		}
		for _, name := range resources {
			if !registered[name] {
				setupLog.Error(nil, "required CRD not found", "groupVersion", gv.String(), "resource", name)
				os.Exit(1)
			}
		}
	}

	// initialize controller-runtime manager with scheme, metrics, health probes, and leader election
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: apiScheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "nstance-operator-leader-election",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
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

	// Add field index on NstanceMachine.status.instanceID for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&infrastructurev1beta1.NstanceMachine{},
		"status.instanceID",
		func(obj client.Object) []string {
			m := obj.(*infrastructurev1beta1.NstanceMachine)
			if m.Status.InstanceID == "" {
				return nil
			}
			return []string{m.Status.InstanceID}
		},
	); err != nil {
		setupLog.Error(err, "unable to create field index for NstanceMachine.status.instanceID")
		os.Exit(1)
	}

	// Create connection provider - will be populated by leader manager after registration
	connProvider := connection.NewProvider()

	// Set up controllers BEFORE mgr.Start() so informers are properly registered
	machinePoolReconciler := &controller.NstanceMachinePoolReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		ConnProvider: connProvider,
	}
	if err := machinePoolReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NstanceMachinePool")
		os.Exit(1)
	}

	if err := (&controller.NstanceClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NstanceCluster")
		os.Exit(1)
	}

	if err := (&controller.NstanceMachineReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		ConnProvider: connProvider,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NstanceMachine")
		os.Exit(1)
	}

	if err := (&controller.OnDemandPodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "OnDemandPod")
		os.Exit(1)
	}

	if err := (&controller.NstanceShardGroupReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		ConnProvider: connProvider,
		Recorder:     mgr.GetEventRecorderFor("nstanceshardgroup-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NstanceShardGroup")
		os.Exit(1)
	}

	if !disableWebhooks {
		if err := (&webhooks.NstanceMachinePoolValidator{}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "NstanceMachinePool")
			os.Exit(1)
		}
	} else {
		setupLog.Info("webhooks disabled via --disable-webhooks flag")
	}

	setupLog.Info("controllers registered")

	// Add leader manager - handles registration and populates connections
	// This runs AFTER leader election, ensuring only one operator performs registration
	if err := mgr.Add(leader.New(mgr.GetClient(), mgr, configPath, connProvider, machinePoolReconciler.SetSyncManager, machinePoolReconciler.SetClusterName)); err != nil {
		setupLog.Error(err, "unable to add leader manager")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
