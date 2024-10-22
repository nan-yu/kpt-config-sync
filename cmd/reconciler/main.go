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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/importer/filesystem"
	"kpt.dev/configsync/pkg/importer/filesystem/cmpath"
	ocmetrics "kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/profiler"
	"kpt.dev/configsync/pkg/reconciler"
	"kpt.dev/configsync/pkg/reconcilermanager"
	"kpt.dev/configsync/pkg/reconcilermanager/controllers"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/util"
	"kpt.dev/configsync/pkg/util/log"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	clusterName = flag.String(flags.clusterName, os.Getenv(reconcilermanager.ClusterNameKey),
		"Cluster name to use for Cluster selection")
	kubeNodeName = flag.String("kube-node-name", os.Getenv(reconcilermanager.KubeNodeNameKey),
		"Kubernetes node name to use for Pub/Sub messages")
	pubSubEnabled = flag.Bool("pubsub-enabled", util.EnvBool(reconcilermanager.PubSubEnabledKey, false),
		"Whether to publish Pub/Sub messages")
	pubSubTopic = flag.String("pubsub-topic", os.Getenv(reconcilermanager.PubSubTopicKey),
		"Name of the Pub/Sub topic")
	scopeStr = flag.String("scope", os.Getenv(reconcilermanager.ScopeKey),
		"Scope of the reconciler, either a namespace or ':root'.")
	syncName = flag.String("sync-name", os.Getenv(reconcilermanager.SyncNameKey),
		"Name of the RootSync or RepoSync object.")
	reconcilerName = flag.String("reconciler-name", os.Getenv(reconcilermanager.ReconcilerNameKey),
		"Name of the reconciler Deployment.")

	// source configuration flags. These values originate in the ConfigManagement and
	// configure git-sync/oci-sync to clone the desired repository/reference we want.
	sourceType = flag.String("source-type", os.Getenv(reconcilermanager.SourceTypeKey),
		"The type of repo being synced, must be git or oci or helm.")
	sourceRepo = flag.String("source-repo", os.Getenv(reconcilermanager.SourceRepoKey),
		"The URL of the git or OCI repo being synced.")
	sourceBranch = flag.String("source-branch", os.Getenv(reconcilermanager.SourceBranchKey),
		"The branch of the git repo being synced.")
	sourceRev = flag.String("source-rev", os.Getenv(reconcilermanager.SourceRevKey),
		"The reference we're syncing to in the repo. Could be a specific commit or a chart version.")
	syncDir = flag.String("sync-dir", os.Getenv(reconcilermanager.SyncDirKey),
		"The relative path of the root configuration directory within the repo.")

	// Performance tuning flags.
	sourceDir = flag.String(flags.sourceDir, "/repo/source/rev",
		"The absolute path in the container running the reconciler to the clone of the source repo.")
	repoRootDir = flag.String(flags.repoRootDir, "/repo",
		"The absolute path in the container running the reconciler to the repo root directory.")
	hydratedRootDir = flag.String(flags.hydratedRootDir, "/repo/hydrated",
		"The absolute path in the container running the reconciler to the hydrated root directory.")
	hydratedLinkDir = flag.String("hydrated-link", "rev",
		"The name of (a symlink to) the source directory under --hydrated-root, which contains the hydrated configs")
	fightDetectionThreshold = flag.Float64(
		"fight-detection-threshold", 5.0,
		"The rate of updates per minute to an API Resource at which the Syncer logs warnings about too many updates to the resource.")
	resyncPeriod = flag.Duration("resync-period", configsync.DefaultReconcilerResyncPeriod,
		"Period of time between forced re-syncs from source (even without a new commit).")
	workers = flag.Int("workers", 1,
		"Number of concurrent remediator workers to run at once.")
	pollingPeriod = flag.Duration("filesystem-polling-period",
		controllers.PollingPeriod(reconcilermanager.ReconcilerPollingPeriod, configsync.DefaultReconcilerPollingPeriod),
		"Period of time between checking the filesystem for source updates to sync.")

	// Root-Repo-only flags. If set for a Namespace-scoped Reconciler, causes the Reconciler to fail immediately.
	sourceFormat = flag.String(flags.sourceFormat, os.Getenv(filesystem.SourceFormatKey),
		"The format of the repository.")
	// Applier flag, Make the reconcile/prune timeout configurable
	reconcileTimeout = flag.String(flags.reconcileTimeout, os.Getenv(reconcilermanager.ReconcileTimeout), "The timeout of applier reconcile and prune tasks")
	// Enable the applier to inject actuation status data into the ResourceGroup object
	statusMode = flag.String(flags.statusMode, os.Getenv(reconcilermanager.StatusMode),
		"When the value is enabled or empty, the applier injects actuation status data into the ResourceGroup object")

	apiServerTimeout = flag.String("api-server-timeout", os.Getenv(reconcilermanager.APIServerTimeout), "The client-side timeout for requests to the API server")

	debug = flag.Bool("debug", false,
		"Enable debug mode, panicking in many scenarios where normally an InternalError would be logged. "+
			"Do not use in production.")

	renderingEnabled  = flag.Bool("rendering-enabled", util.EnvBool(reconcilermanager.RenderingEnabled, false), "")
	namespaceStrategy = flag.String(flags.namespaceStrategy, util.EnvString(reconcilermanager.NamespaceStrategy, ""),
		fmt.Sprintf("Set the namespace strategy for the reconciler. Must be %s or %s. Default: %s.",
			configsync.NamespaceStrategyImplicit, configsync.NamespaceStrategyExplicit, configsync.NamespaceStrategyImplicit))

	dynamicNSSelectorEnabled = flag.Bool("dynamic-ns-selector-enabled", util.EnvBool(reconcilermanager.DynamicNSSelectorEnabled, false), "")

	webhookEnabled = flag.Bool("webhook-enabled", util.EnvBool(reconcilermanager.WebhookEnabled, false), "")
)

var flags = struct {
	sourceDir         string
	repoRootDir       string
	hydratedRootDir   string
	clusterName       string
	sourceFormat      string
	statusMode        string
	reconcileTimeout  string
	namespaceStrategy string
}{
	repoRootDir:       "repo-root",
	sourceDir:         "source-dir",
	hydratedRootDir:   "hydrated-root",
	clusterName:       "cluster-name",
	sourceFormat:      reconcilermanager.SourceFormat,
	statusMode:        "status-mode",
	reconcileTimeout:  "reconcile-timeout",
	namespaceStrategy: "namespace-strategy",
}

func main() {
	log.Setup()
	profiler.Service()
	ctrl.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))

	if *debug {
		status.EnablePanicOnMisuse()
	}

	// Register the OpenCensus views
	if err := ocmetrics.RegisterReconcilerMetricsViews(); err != nil {
		klog.Fatalf("Failed to register OpenCensus views: %v", err)
	}

	// Register the OC Agent exporter
	oce, err := ocmetrics.RegisterOCAgentExporter(reconcilermanager.Reconciler)
	if err != nil {
		klog.Fatalf("Failed to register the OC Agent exporter: %v", err)
	}

	defer func() {
		if err := oce.Stop(); err != nil {
			klog.Fatalf("Unable to stop the OC Agent exporter: %v", err)
		}
	}()

	absRepoRoot, err := cmpath.AbsoluteOS(*repoRootDir)
	if err != nil {
		klog.Fatalf("%s must be an absolute path: %v", flags.repoRootDir, err)
	}

	// Normalize syncDirRelative.
	// Some users specify the directory as if the root of the repository is "/".
	// Strip this from the front of the passed directory so behavior is as
	// expected.
	dir := strings.TrimPrefix(*syncDir, "/")
	relSyncDir := cmpath.RelativeOS(dir)
	absSourceDir, err := cmpath.AbsoluteOS(*sourceDir)
	if err != nil {
		klog.Fatalf("%s must be an absolute path: %v", flags.sourceDir, err)
	}

	scope := declared.Scope(*scopeStr)
	err = scope.Validate()
	if err != nil {
		klog.Fatal(err)
	}

	opts := reconciler.Options{
		ClusterName:              *clusterName,
		KubeNodeName:             *kubeNodeName,
		PubSubEnabled:            *pubSubEnabled,
		PubSubTopic:              *pubSubTopic,
		FightDetectionThreshold:  *fightDetectionThreshold,
		NumWorkers:               *workers,
		ReconcilerScope:          scope,
		ResyncPeriod:             *resyncPeriod,
		PollingPeriod:            *pollingPeriod,
		RetryPeriod:              configsync.DefaultReconcilerRetryPeriod,
		StatusUpdatePeriod:       configsync.DefaultReconcilerSyncStatusUpdatePeriod,
		SourceRoot:               absSourceDir,
		RepoRoot:                 absRepoRoot,
		HydratedRoot:             *hydratedRootDir,
		HydratedLink:             *hydratedLinkDir,
		SourceRev:                *sourceRev,
		SourceBranch:             *sourceBranch,
		SourceType:               configsync.SourceType(*sourceType),
		SourceRepo:               *sourceRepo,
		SyncDir:                  relSyncDir,
		SyncName:                 *syncName,
		ReconcilerName:           *reconcilerName,
		StatusMode:               *statusMode,
		ReconcileTimeout:         *reconcileTimeout,
		APIServerTimeout:         *apiServerTimeout,
		RenderingEnabled:         *renderingEnabled,
		DynamicNSSelectorEnabled: *dynamicNSSelectorEnabled,
		WebhookEnabled:           *webhookEnabled,
	}

	if scope == declared.RootScope {
		// Default to "hierarchy" if unset.
		format := configsync.SourceFormat(*sourceFormat)
		if format == "" {
			format = configsync.SourceFormatHierarchy
		}
		// Default to "implicit" if unset.
		nsStrat := configsync.NamespaceStrategy(*namespaceStrategy)
		if nsStrat == "" {
			nsStrat = configsync.NamespaceStrategyImplicit
		}

		klog.Info("Starting reconciler for: root")
		opts.RootOptions = &reconciler.RootOptions{
			SourceFormat:      format,
			NamespaceStrategy: nsStrat,
		}
	} else {
		klog.Infof("Starting reconciler for: %s", scope)

		if *sourceFormat != "" {
			klog.Fatalf("Flag %s and environment variable %s must not be passed to a Namespace reconciler",
				flags.sourceFormat, filesystem.SourceFormatKey)
		}
		if *namespaceStrategy != "" {
			klog.Fatalf("Flag %s and environment variable %s must not be passed to a Namespace reconciler",
				flags.namespaceStrategy, reconcilermanager.NamespaceStrategy)
		}
	}
	reconciler.Run(opts)
}
