// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command controller-runtime runs a controller that manages arbitrary workloads
// through Karta definitions stored in the cluster.
//
// Unlike the offline quickstart example, this controller installs into a real
// cluster (for example a Kind cluster), watches live workloads, and reacts to
// every change. The workload types it manages are configured at runtime with
// the --watch-gvk flag, and each type's structure is read from its Karta custom
// resource rather than from code. Managing a new workload type needs no code
// change: add its GVK to --watch-gvk and apply its Karta object (and grant RBAC
// for the new resource).
//
// See README.md for the end-to-end Kind walkthrough.
package main

import (
	"flag"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// defaultWatchGVKs is the out-of-the-box workload set. Override with --watch-gvk
// to manage more types without rebuilding the controller.
const defaultWatchGVKs = "leaderworkerset.x-k8s.io/v1/LeaderWorkerSet"

func main() {
	var metricsAddr, probeAddr, watchGVKs string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address the metrics endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address the health probe binds to")
	flag.StringVar(&watchGVKs, "watch-gvk", defaultWatchGVKs,
		"comma-separated workload GVKs to manage, each as group/version/kind (core group is empty, e.g. /v1/Pod)")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("setup")

	gvks, err := parseGVKs(watchGVKs)
	if err != nil {
		logger.Error(err, "parse --watch-gvk")
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Error(err, "add client-go scheme")
		os.Exit(1)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		logger.Error(err, "add corev1 scheme")
		os.Exit(1)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		logger.Error(err, "add Karta scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         false,
	})
	if err != nil {
		logger.Error(err, "create manager")
		os.Exit(1)
	}

	for _, gvk := range gvks {
		reconciler := &WorkloadReconciler{
			Client:   mgr.GetClient(),
			Recorder: mgr.GetEventRecorder("generic-controller"),
			GVK:      gvk,
		}
		if err := reconciler.SetupWithManager(mgr); err != nil {
			logger.Error(err, "set up reconciler", "gvk", gvk.String())
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "add health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "add ready check")
		os.Exit(1)
	}

	logger.Info("starting controller", "watchGVKs", watchGVKs)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "run manager")
		os.Exit(1)
	}
}
