// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconciler

import (
	"context"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	"k8s.io/utils/clock"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/applier"
	"kpt.dev/configsync/pkg/applyset"
	"kpt.dev/configsync/pkg/client/restconfig"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/importer/filesystem"
	"kpt.dev/configsync/pkg/importer/filesystem/cmpath"
	"kpt.dev/configsync/pkg/importer/reader"
	"kpt.dev/configsync/pkg/parse"
	"kpt.dev/configsync/pkg/parse/events"
	"kpt.dev/configsync/pkg/reconciler/finalizer"
	"kpt.dev/configsync/pkg/reconciler/namespacecontroller"
	"kpt.dev/configsync/pkg/reconcilermanager/controllers"
	"kpt.dev/configsync/pkg/remediator"
	"kpt.dev/configsync/pkg/remediator/conflict"
	"kpt.dev/configsync/pkg/remediator/watch"
	syncerclient "kpt.dev/configsync/pkg/syncer/client"
	"kpt.dev/configsync/pkg/syncer/metrics"
	"kpt.dev/configsync/pkg/syncer/reconcile"
	"kpt.dev/configsync/pkg/syncer/reconcile/fight"
	"kpt.dev/configsync/pkg/util"
	utilwatch "kpt.dev/configsync/pkg/util/watch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

// Options contains the settings for a reconciler process.
type Options struct {
	// ClusterName is the name of the cluster we are parsing configuration for.
	ClusterName string
	// KubeNodeName is the name of the Kubernetes node.
	KubeNodeName string
	// PubSubEnabled indicates whether to publish PubSub messages
	PubSubEnabled bool
	// PubSubTopic is the name of the PubSub topic
	PubSubTopic string
	// FightDetectionThreshold is the rate of updates per minute to an API
	// Resource at which the reconciler will log warnings about too many updates
	// to the resource.
	FightDetectionThreshold float64
	// NumWorkers is the number of concurrent remediator workers to run at once.
	// Each worker pulls resources off of the work queue and remediates them one
	// at a time.
	NumWorkers int
	// ReconcilerScope is the scope of resources which the reconciler will manage.
	// Currently this can either be a namespace or the root scope which allows a
	// cluster admin to manage the entire cluster.
	//
	// At most one Reconciler may have a given value for Scope on a cluster. More
	// than one results in undefined behavior.
	ReconcilerScope declared.Scope
	// SyncName is the name of the RootSync or RepoSync object.
	SyncName string
	// ReconcilerName is the name of the Reconciler Deployment.
	ReconcilerName string
	// ResyncPeriod is the period of time between forced re-sync from source (even
	// without a new commit).
	ResyncPeriod time.Duration
	// PollingPeriod is the period of time between checking the filesystem for
	// source updates to sync.
	PollingPeriod time.Duration
	// RetryPeriod is the period of time between checking the filesystem for
	// source updates to sync, after an error.
	RetryPeriod time.Duration
	// StatusUpdatePeriod is how long the parser waits between updates of the
	// sync status, to account for management conflict errors from the remediator.
	StatusUpdatePeriod time.Duration
	// SourceRoot is the absolute path to the source repository.
	// Usually contains a symlink that must be resolved every time before parsing.
	SourceRoot cmpath.Absolute
	// HydratedRoot is the absolute path to the hydrated configs.
	// If hydration is not performed, it will be an empty path.
	HydratedRoot string
	// RepoRoot is the absolute path to the parent directory of SourceRoot and HydratedRoot.
	RepoRoot cmpath.Absolute
	// HydratedLink is the relative path to the hydrated root.
	// It is a symlink that links to the hydrated configs under the hydrated root dir.
	HydratedLink string
	// SourceRev is the git revision or a helm chart version being synced.
	SourceRev string
	// SourceBranch is the git branch being synced.
	SourceBranch string
	// SourceRepo is the git or OCI or Helm repo being synced.
	SourceRepo string
	// SourceType is the type of the source repository, must be git or oci or Helm.
	SourceType configsync.SourceType
	// SyncDir is the relative path to the configurations in the source.
	SyncDir cmpath.Relative
	// StatusMode controls the kpt applier to inject the actuation status data or not
	StatusMode string
	// ReconcileTimeout controls the reconcile/prune Timeout in kpt applier
	ReconcileTimeout string
	// APIServerTimeout is the client-side timeout used for talking to the API server
	APIServerTimeout string
	// RenderingEnabled indicates whether the reconciler Pod is currently running
	// with the hydration-controller.
	RenderingEnabled bool
	// RootOptions is the set of options to fill in if this is configuring the
	// Root reconciler.
	// Unset for Namespace repositories.
	*RootOptions
	// DynamicNSSelectorEnabled indicates whether there exists at least one
	// NamespaceSelector using the dynamic mode, which requires Namespace
	// controller running to watch Namespace events.
	DynamicNSSelectorEnabled bool
	// WebhookEnabled is indicates whether the Admission Webhook is currently
	// installed and running
	WebhookEnabled bool
}

// RootOptions are the options specific to parsing Root repositories.
type RootOptions struct {
	// SourceFormat is how the Root repository is structured.
	SourceFormat configsync.SourceFormat
	// NamespaceStrategy indicates the NamespaceStrategy used by this reconciler.
	NamespaceStrategy configsync.NamespaceStrategy
}

// Run configures and starts the various components of a reconciler process.
func Run(opts Options) {
	fight.SetFightThreshold(opts.FightDetectionThreshold)

	// Get a config to talk to the apiserver.
	apiServerTimeout, err := time.ParseDuration(opts.APIServerTimeout)
	if err != nil {
		klog.Fatalf("Error parsing applier reconcile/prune task timeout: %v", err)
	}
	if apiServerTimeout <= 0 {
		klog.Fatalf("Invalid apiServerTimeout: %v, timeout should be positive", apiServerTimeout)
	}
	cfg, err := restconfig.NewRestConfig(apiServerTimeout)
	if err != nil {
		klog.Fatalf("Error creating rest config: %v", err)
	}

	configFlags, err := restconfig.NewConfigFlags(cfg)
	if err != nil {
		klog.Fatalf("Error creating config flags from rest config: %v", err)
	}

	discoveryClient, err := configFlags.ToDiscoveryClient()
	if err != nil {
		klog.Fatalf("Error creating discovery client: %v", err)
	}

	mapper, err := utilwatch.ReplaceOnResetRESTMapperFromConfig(cfg)
	if err != nil {
		klog.Fatalf("Error creating resettable rest mapper: %v", err)
	}

	cl, err := client.New(cfg, client.Options{
		Scheme: core.Scheme,
		Mapper: mapper,
	})
	if err != nil {
		klog.Fatalf("failed to create client: %v", err)
	}

	// Configure the Applier.
	applySetID := applyset.IDFromSync(opts.SyncName, opts.ReconcilerScope)
	genericClient := syncerclient.New(cl, metrics.APICallDuration)
	baseApplier, err := reconcile.NewApplierForMultiRepo(cfg, genericClient, applySetID)
	if err != nil {
		klog.Fatalf("Instantiating Applier: %v", err)
	}

	reconcileTimeout, err := time.ParseDuration(opts.ReconcileTimeout)
	if err != nil {
		klog.Fatalf("Error parsing applier reconcile/prune task timeout: %v", err)
	}
	if reconcileTimeout < 0 {
		klog.Fatalf("Invalid reconcileTimeout: %v, timeout should not be negative", reconcileTimeout)
	}
	clientSet, err := applier.NewClientSet(cl, configFlags, opts.StatusMode, applySetID)
	if err != nil {
		klog.Fatalf("Error creating clients: %v", err)
	}
	supervisor, err := applier.NewSupervisor(clientSet, opts.ReconcilerScope, opts.SyncName, reconcileTimeout)
	if err != nil {
		klog.Fatalf("Error creating applier: %v", err)
	}

	// Configure the Remediator.
	decls := &declared.Resources{}

	// Get a separate config for the remediator to talk to the apiserver since
	// we want a longer REST config timeout for the remediator to avoid restarting
	// idle watches too frequently.
	cfgForWatch, err := restconfig.NewRestConfig(watch.RESTConfigTimeout)
	if err != nil {
		klog.Fatalf("Error creating rest config for the remediator: %v", err)
	}
	dynamicClient, err := dynamic.NewForConfig(cfgForWatch)
	if err != nil {
		klog.Fatalf("Error creating DynamicClient for the remediator: %v", err)
	}
	lwFactory := &watch.DynamicListerWatcherFactory{
		DynamicClient: dynamicClient,
		Mapper:        mapper,
	}
	watcherFactory := watch.WatcherFactoryFromListerWatcherFactory(lwFactory.ListerWatcher)
	crdController := &controllers.CRDController{}
	conflictHandler := conflict.NewHandler()
	fightHandler := fight.NewHandler()

	rem, err := remediator.New(opts.ReconcilerScope, opts.SyncName, watcherFactory, mapper, baseApplier, conflictHandler, fightHandler, crdController, decls, opts.NumWorkers)
	if err != nil {
		klog.Fatalf("Instantiating Remediator: %v", err)
	}

	// Configure the Parser.
	var parser parse.Parser
	fs := parse.FileSource{
		SourceDir:    opts.SourceRoot,
		RepoRoot:     opts.RepoRoot,
		HydratedRoot: opts.HydratedRoot,
		HydratedLink: opts.HydratedLink,
		SyncDir:      opts.SyncDir,
		SourceType:   opts.SourceType,
		SourceRepo:   opts.SourceRepo,
		SourceBranch: opts.SourceBranch,
		SourceRev:    opts.SourceRev,
	}

	parseOpts := &parse.Options{
		Clock:              clock.RealClock{},
		Parser:             filesystem.NewParser(&reader.File{}),
		ClusterName:        opts.ClusterName,
		KubeNodeName:       opts.KubeNodeName,
		PubSubEnabled:      opts.PubSubEnabled,
		PubSubTopic:        opts.PubSubTopic,
		Client:             cl,
		ReconcilerName:     opts.ReconcilerName,
		SyncName:           opts.SyncName,
		StatusUpdatePeriod: opts.StatusUpdatePeriod,
		DiscoveryInterface: discoveryClient,
		RenderingEnabled:   opts.RenderingEnabled,
		Files:              parse.Files{FileSource: fs},
		WebhookEnabled:     opts.WebhookEnabled,
		Updater: parse.Updater{
			Scope:          opts.ReconcilerScope,
			Resources:      decls,
			Applier:        supervisor,
			Remediator:     rem,
			SyncErrorCache: parse.NewSyncErrorCache(conflictHandler, fightHandler),
		},
	}
	// Only instantiate the converter when the webhook is enabled because the
	// instantiation pulls fresh schemas from the openapi discovery endpoint.
	if opts.WebhookEnabled {
		parseOpts.Converter, err = declared.NewValueConverter(discoveryClient)
		if err != nil {
			klog.Fatalf("Instantiating converter: %v", err)
		}
	}

	// Use the builder to build a set of event publishers for parser.Run.
	pgBuilder := &events.PublishingGroupBuilder{
		Clock:                  parseOpts.Clock,
		SyncPeriod:             opts.PollingPeriod,
		SyncWithReimportPeriod: opts.ResyncPeriod,
		StatusUpdatePeriod:     opts.StatusUpdatePeriod,
		// TODO: Shouldn't this use opts.RetryPeriod as the initial duration?
		// Limit to 12 retries, with no max retry duration.
		RetryBackoff: util.BackoffWithDurationAndStepLimit(0, 12),
	}

	var nsControllerState *namespacecontroller.State
	if opts.ReconcilerScope == declared.RootScope {
		rootOpts := &parse.RootOptions{
			SourceFormat:             opts.SourceFormat,
			NamespaceStrategy:        opts.NamespaceStrategy,
			DynamicNSSelectorEnabled: opts.DynamicNSSelectorEnabled,
		}
		if opts.DynamicNSSelectorEnabled {
			// Only set nsControllerState when dynamic NamespaceSelector is
			// enabled on RootSyncs.
			// RepoSync can't manage NamespaceSelectors.
			nsControllerState = namespacecontroller.NewState()
			rootOpts.NSControllerState = nsControllerState
			// Enable namespace events (every second)
			// TODO: Trigger namespace events with a buffered channel from the NamespaceController
			pgBuilder.NamespaceControllerPeriod = time.Second
		}
		parser = parse.NewRootRunner(parseOpts, rootOpts)
	} else {
		parser = parse.NewNamespaceRunner(parseOpts)
	}

	// Start listening to signals
	signalCtx := signals.SetupSignalHandler()

	// Create the ControllerManager
	ctrl.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))
	mgrOptions := ctrl.Options{
		Scheme: core.Scheme,
		MapperProvider: func(_ *rest.Config, _ *http.Client) (meta.RESTMapper, error) {
			return mapper, nil
		},
		BaseContext: func() context.Context {
			return signalCtx
		},
	}
	// For Namespaced Reconcilers, set the default namespace to watch.
	// Otherwise, all namespaced informers will watch at the cluster-scope.
	// This prevents Namespaced Reconcilers from needing cluster-scoped read
	// permissions.
	if opts.ReconcilerScope != declared.RootScope {
		mgrOptions.Cache.DefaultNamespaces = map[string]cache.Config{
			string(opts.ReconcilerScope): {},
		}
	}
	mgr, err := ctrl.NewManager(cfgForWatch, mgrOptions)
	if err != nil {
		klog.Fatalf("Instantiating Controller Manager: %v", err)
	}

	crdControllerLogger := textlogger.NewLogger(textlogger.NewConfig()).WithName("controllers").WithName("CRD")
	crdMetaController := controllers.NewCRDMetaController(crdController,
		mgr.GetCache(), mapper, crdControllerLogger)
	if err := crdMetaController.Register(mgr); err != nil {
		klog.Fatalf("Instantiating CRD Controller: %v", err)
	}

	// This cancelFunc will be used by the Finalizer to stop all the other
	// controllers (Parser & Remediator).
	ctx, stopControllers := context.WithCancel(signalCtx)
	// This channel will be closed when all the other controllers have exited,
	// signalling for the finalizer to continue.
	continueChanForFinalizer := make(chan struct{})

	// Create the Finalizer
	// The caching client built by the controller-manager doesn't update
	// the GET cache on UPDATE/PATCH. So we need to use the non-caching client
	// for the finalizer, which does GET/LIST after UPDATE/PATCH.
	f := finalizer.New(opts.ReconcilerScope, supervisor, cl, // non-caching client
		stopControllers, continueChanForFinalizer)

	// Create the Finalizer Controller
	finalizerController := &finalizer.Controller{
		SyncScope: opts.ReconcilerScope,
		SyncName:  opts.SyncName,
		Client:    mgr.GetClient(), // caching client
		Scheme:    mgr.GetScheme(),
		Mapper:    mgr.GetRESTMapper(),
		Finalizer: f,
	}

	// Register the Finalizer Controller
	if err := finalizerController.SetupWithManager(mgr); err != nil {
		klog.Fatalf("Instantiating Finalizer: %v", err)
	}

	// Only create and register the Namespace Controller when the flag is enabled.
	// If the flag is disabled, no need to watch the Namespace events.
	// The NamespaceSelector will dis-select those dynamic/on-cluster Namespaces.
	if opts.DynamicNSSelectorEnabled {
		nsController := namespacecontroller.New(cl, nsControllerState)

		// Register the Namespace Controller
		// The controller will stop when the controller-manager shuts down.
		if err := nsController.SetupWithManager(mgr); err != nil {
			klog.Fatalf("Instantiating Namespace Controller: %v", err)
		}
	}

	klog.Info("Starting ControllerManager")
	// TODO: Once everything is using the controller-manager, move mgr.Start to the top level.
	doneChanForManager := make(chan struct{})
	go func() {
		defer func() {
			// If the manager returned, there was either an error or a term/kill
			// signal. So stop the other controllers, if not already stopped.
			stopControllers()
			close(doneChanForManager) // Signal thread completion
		}()
		err := mgr.Start(signalCtx) // blocks on signalCtx.Done()
		if err != nil {
			klog.Errorf("Starting ControllerManager: %v", err)
			// klog.Fatalf calls os.Exit, which doesn't trigger defer funcs.
			// So we're using klog.Error instead, for now.
			// TODO: Once this is top-level, just call klog.Fatalf
		}
	}()

	klog.Info("Starting Remediator")
	// TODO: Convert the Remediator to use the controller-manager framework.
	doneChanForRemediator := rem.Start(ctx) // non-blocking

	klog.Info("Starting Parser")
	// TODO: Convert the Parser to use the controller-manager framework.
	// Funnel events from the publishers to the subscriber.
	funnel := &events.Funnel{
		Publishers: pgBuilder.Build(),
		// Wrap the parser with an event handler that triggers the RunFunc, as needed.
		Subscriber: parse.NewEventHandler(ctx, parser, nsControllerState, parse.DefaultRunFunc),
	}
	doneChForParser := funnel.Start(ctx)

	// Wait until done
	<-doneChForParser
	klog.Info("Parser exited")

	// Wait for Remediator to exit
	<-doneChanForRemediator
	klog.Info("Remediator exited")

	// Unblock the Finalizer to destroy managed resources, if needed.
	close(continueChanForFinalizer)
	// Wait for ControllerManager to exit
	<-doneChanForManager
	klog.Info("Finalizer exited")

	// Wait for exit signal, if not already received.
	// This avoids unnecessary restarts after the finalizer has completed.
	<-signalCtx.Done()
	klog.Info("All controllers exited")
}
