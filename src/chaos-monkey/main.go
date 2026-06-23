package main

import (
	"context"
	"flag"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	chaosv1alpha1 "chaos_monkey/apis/chaos/v1alpha1"
	"chaos_monkey/controller"
	"chaos_monkey/daemon"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(chaosv1alpha1.AddToScheme(scheme))
}

func main() {
	var mode string
	var metricsAddr string
	var nodeName string

	flag.StringVar(&mode, "mode", "controller", "The mode to run in: 'controller' or 'daemon'")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&nodeName, "node-name", os.Getenv("NODE_NAME"), "The name of the node (required in daemon mode).")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if mode == "controller" {
		setupLog.Info("Starting Chaos Monkey Central Controller")
		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme:  scheme,
			Metrics: metricsserver.Options{BindAddress: metricsAddr},
		})
		if err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}

		if err = (&controller.ChaosExperimentReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    ctrl.Log.WithName("controllers").WithName("ChaosExperiment"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ChaosExperiment")
			os.Exit(1)
		}

		setupLog.Info("starting manager")
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			setupLog.Error(err, "problem running manager")
			os.Exit(1)
		}

	} else if mode == "daemon" {
		setupLog.Info("Starting Chaos Monkey Edge Daemon", "nodeName", nodeName)
		if nodeName == "" {
			setupLog.Info("NODE_NAME env var or -node-name flag must be set in daemon mode")
			os.Exit(1)
		}

		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme:  scheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					// spec.nodeName is natively indexed for Pods by the API server
					&corev1.Pod{}: {
						Field: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}),
					},
				},
			},
		})
		if err != nil {
			setupLog.Error(err, "unable to start daemon manager")
			os.Exit(1)
		}

		// Register a field indexer for NodeChaosTask.spec.nodeName so we can
		// filter the local informer cache efficiently without a list-all.
		if err := mgr.GetFieldIndexer().IndexField(
			context.Background(),
			&chaosv1alpha1.NodeChaosTask{},
			"spec.nodeName",
			func(obj client.Object) []string {
				task, ok := obj.(*chaosv1alpha1.NodeChaosTask)
				if !ok {
					return nil
				}
				return []string{task.Spec.NodeName}
			},
		); err != nil {
			setupLog.Error(err, "unable to register NodeChaosTask field indexer")
			os.Exit(1)
		}

		shaper := daemon.NewEBPFTrafficShaper()

		d := daemon.NewChaosDaemonWithShaper(
			mgr.GetClient(),
			mgr.GetScheme(),
			ctrl.Log.WithName("daemons").WithName("ChaosDaemon"),
			nodeName,
			shaper,
		)

		if err = d.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create daemon controller")
			os.Exit(1)
		}

		go func() {
			http.Handle("/metrics", promhttp.Handler())
			setupLog.Info("Starting metrics server", "addr", metricsAddr)
			if err := http.ListenAndServe(metricsAddr, nil); err != nil {
				setupLog.Error(err, "metrics server failed")
			}
		}()

		ctx := ctrl.SetupSignalHandler()
		go d.StartHeartbeat(ctx)

		setupLog.Info("starting daemon manager")
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "problem running daemon manager")
			os.Exit(1)
		}
	} else {
		setupLog.Info("invalid mode. Must be 'controller' or 'daemon'")
		os.Exit(1)
	}
}
