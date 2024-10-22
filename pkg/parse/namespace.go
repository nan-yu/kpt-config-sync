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

package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/importer/analyzer/ast"
	"kpt.dev/configsync/pkg/importer/reader"
	"kpt.dev/configsync/pkg/metadata"
	"kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/pubsub"
	"kpt.dev/configsync/pkg/reposync"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/util/compare"
	utildiscovery "kpt.dev/configsync/pkg/util/discovery"
	"kpt.dev/configsync/pkg/validate"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewNamespaceRunner creates a new runnable parser for parsing a Namespace repo.
func NewNamespaceRunner(opts *Options) Parser {
	return &namespace{
		Options: opts,
	}
}

type namespace struct {
	*Options
}

var _ Parser = &namespace{}

func (p *namespace) options() *Options {
	return p.Options
}

// parseSource implements the Parser interface
func (p *namespace) parseSource(ctx context.Context, state *sourceState) ([]ast.FileObject, status.MultiError) {
	p.mux.Lock()
	defer p.mux.Unlock()

	filePaths := reader.FilePaths{
		RootDir:   state.syncDir,
		PolicyDir: p.SyncDir,
		Files:     state.files,
	}
	crds, err := p.declaredCRDs()
	if err != nil {
		return nil, err
	}
	builder := utildiscovery.ScoperBuilder(p.DiscoveryInterface)

	klog.Infof("Parsing files from source dir: %s", state.syncDir.OSPath())
	objs, err := p.Parser.Parse(filePaths)
	if err != nil {
		return nil, err
	}

	options := validate.Options{
		ClusterName:  p.ClusterName,
		SyncName:     p.SyncName,
		PolicyDir:    p.SyncDir,
		PreviousCRDs: crds,
		BuildScoper:  builder,
		Converter:    p.Converter,
		// Namespaces and NamespaceSelectors should not be declared in a namespace repo.
		// So disable the API call and dynamic mode of NamespaceSelector.
		AllowAPICall:             false,
		DynamicNSSelectorEnabled: false,
		WebhookEnabled:           p.WebhookEnabled,
		FieldManager:             configsync.FieldManager,
	}
	options = OptionsForScope(options, p.Scope)

	objs, err = validate.Unstructured(ctx, p.Client, objs, options)

	if status.HasBlockingErrors(err) {
		return nil, err
	}

	// Duplicated with root.go.
	e := addAnnotationsAndLabels(objs, p.Scope, p.SyncName, p.sourceContext(), state.commit)
	if e != nil {
		err = status.Append(err, status.InternalErrorf("unable to add annotations and labels: %v", e))
		return nil, err
	}
	return objs, err
}

// setSourceStatus implements the Parser interface
//
// setSourceStatus sets the source status with a given source state and set of errors.  If errs is empty, all errors
// will be removed from the status.
func (p *namespace) setSourceStatus(ctx context.Context, newStatus *SourceStatus) error {
	p.mux.Lock()
	defer p.mux.Unlock()
	return p.setSourceStatusWithRetries(ctx, newStatus, defaultDenominator)
}

func (p *namespace) setSourceStatusWithRetries(ctx context.Context, newStatus *SourceStatus, denominator int) error {
	if denominator <= 0 {
		return fmt.Errorf("The denominator must be a positive number")
	}
	// The main idea here is an error-robust way of surfacing to the user that
	// we're having problems reading from our local clone of their source repository.
	// This can happen when Kubernetes does weird things with mounted filesystems,
	// or if an attacker tried to maliciously change the cluster's record of the
	// source of truth.
	var rs v1beta1.RepoSync
	if err := p.Client.Get(ctx, reposync.ObjectKey(p.Scope, p.SyncName), &rs); err != nil {
		return status.APIServerError(err, "failed to get RepoSync for parser")
	}

	currentRS := rs.DeepCopy()

	setSourceStatusFields(&rs.Status.Source, newStatus, denominator)

	continueSyncing := (rs.Status.Source.ErrorSummary.TotalCount == 0)
	var errorSource []v1beta1.ErrorSource
	if len(rs.Status.Source.Errors) > 0 {
		errorSource = []v1beta1.ErrorSource{v1beta1.SourceError}
	}
	reposync.SetSyncing(&rs, continueSyncing, "Source", "Source", newStatus.Commit, errorSource, rs.Status.Source.ErrorSummary, newStatus.LastUpdate)

	// Avoid unnecessary status updates.
	if !currentRS.Status.Source.LastUpdate.IsZero() && cmp.Equal(currentRS.Status, rs.Status, compare.IgnoreTimestampUpdates) {
		klog.V(5).Infof("Skipping source status update for RepoSync %s/%s", rs.Namespace, rs.Name)
		return nil
	}

	csErrs := status.ToCSE(newStatus.Errs)
	metrics.RecordReconcilerErrors(ctx, "source", csErrs)
	metrics.RecordPipelineError(ctx, configsync.RepoSyncName, "source", len(csErrs))
	if len(csErrs) > 0 {
		klog.Infof("New source errors for RepoSync %s/%s: %+v",
			rs.Namespace, rs.Name, csErrs)
	}

	if klog.V(5).Enabled() {
		klog.V(5).Infof("Updating source status:\nDiff (- Removed, + Added):\n%s",
			cmp.Diff(currentRS.Status, rs.Status))
	}

	if err := p.Client.Status().Update(ctx, &rs, client.FieldOwner(configsync.FieldManager)); err != nil {
		// If the update failure was caused by the size of the RepoSync object, we would truncate the errors and retry.
		if isRequestTooLargeError(err) {
			klog.Infof("Failed to update RepoSync source status (total error count: %d, denominator: %d): %s.", rs.Status.Source.ErrorSummary.TotalCount, denominator, err)
			return p.setSourceStatusWithRetries(ctx, newStatus, denominator*2)
		}
		return status.APIServerError(err, "failed to update RepoSync source status from Parser")
	}
	return nil
}

func (p *namespace) setRequiresRendering(ctx context.Context, renderingRequired bool) error {
	rs := &v1beta1.RepoSync{}
	if err := p.Client.Get(ctx, reposync.ObjectKey(p.Scope, p.SyncName), rs); err != nil {
		return status.APIServerError(err, "failed to get RepoSync for Parser")
	}
	newVal := strconv.FormatBool(renderingRequired)
	if core.GetAnnotation(rs, metadata.RequiresRenderingAnnotationKey) == newVal {
		// avoid unnecessary updates
		return nil
	}
	existing := rs.DeepCopy()
	core.SetAnnotation(rs, metadata.RequiresRenderingAnnotationKey, newVal)
	return p.Client.Patch(ctx, rs, client.MergeFrom(existing), client.FieldOwner(configsync.FieldManager))
}

// setRenderingStatus implements the Parser interface
func (p *namespace) setRenderingStatus(ctx context.Context, oldStatus, newStatus *RenderingStatus) error {
	if oldStatus.Equals(newStatus) {
		return nil
	}

	p.mux.Lock()
	defer p.mux.Unlock()
	return p.setRenderingStatusWithRetries(ctx, newStatus, defaultDenominator)
}

func (p *namespace) setRenderingStatusWithRetries(ctx context.Context, newStatus *RenderingStatus, denominator int) error {
	if denominator <= 0 {
		return fmt.Errorf("The denominator must be a positive number")
	}

	var rs v1beta1.RepoSync
	if err := p.Client.Get(ctx, reposync.ObjectKey(p.Scope, p.SyncName), &rs); err != nil {
		return status.APIServerError(err, "failed to get RepoSync for parser")
	}

	currentRS := rs.DeepCopy()

	setRenderingStatusFields(&rs.Status.Rendering, newStatus, denominator)

	continueSyncing := (rs.Status.Rendering.ErrorSummary.TotalCount == 0)
	var errorSource []v1beta1.ErrorSource
	if len(rs.Status.Rendering.Errors) > 0 {
		errorSource = []v1beta1.ErrorSource{v1beta1.RenderingError}
	}
	reposync.SetSyncing(&rs, continueSyncing, "Rendering", newStatus.Message, newStatus.Commit, errorSource, rs.Status.Rendering.ErrorSummary, newStatus.LastUpdate)

	// Avoid unnecessary status updates.
	if !currentRS.Status.Rendering.LastUpdate.IsZero() && cmp.Equal(currentRS.Status, rs.Status, compare.IgnoreTimestampUpdates) {
		klog.V(5).Infof("Skipping rendering status update for RepoSync %s/%s", rs.Namespace, rs.Name)
		return nil
	}

	csErrs := status.ToCSE(newStatus.Errs)
	metrics.RecordReconcilerErrors(ctx, "rendering", csErrs)
	metrics.RecordPipelineError(ctx, configsync.RepoSyncName, "rendering", len(csErrs))
	if len(csErrs) > 0 {
		klog.Infof("New rendering errors for RepoSync %s/%s: %+v",
			rs.Namespace, rs.Name, csErrs)
	}

	if klog.V(5).Enabled() {
		klog.V(5).Infof("Updating rendering status:\nDiff (- Removed, + Added):\n%s",
			cmp.Diff(currentRS.Status, rs.Status))
	}

	if err := p.Client.Status().Update(ctx, &rs, client.FieldOwner(configsync.FieldManager)); err != nil {
		// If the update failure was caused by the size of the RepoSync object, we would truncate the errors and retry.
		if isRequestTooLargeError(err) {
			klog.Infof("Failed to update RepoSync rendering status (total error count: %d, denominator: %d): %s.", rs.Status.Rendering.ErrorSummary.TotalCount, denominator, err)
			return p.setRenderingStatusWithRetries(ctx, newStatus, denominator*2)
		}
		return status.APIServerError(err, "failed to update RepoSync rendering status from parser")
	}
	return nil
}

// ReconcilerStatusFromCluster gets the RepoSync sync status from the cluster.
func (p *namespace) ReconcilerStatusFromCluster(ctx context.Context) (*ReconcilerStatus, error) {
	rs := &v1beta1.RepoSync{}
	if err := p.Client.Get(ctx, reposync.ObjectKey(p.Scope, p.SyncName), rs); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, status.APIServerError(err, fmt.Sprintf("failed to get the RepoSync object for the %v namespace", p.Scope))
	}

	// Read Syncing condition
	syncing := false
	var syncingConditionLastUpdate metav1.Time
	for _, condition := range rs.Status.Conditions {
		if condition.Type == v1beta1.RepoSyncSyncing {
			if condition.Status == metav1.ConditionTrue {
				syncing = true
			}
			syncingConditionLastUpdate = condition.LastUpdateTime
			break
		}
	}

	return reconcilerStatusFromRSyncStatus(rs.Status.Status, p.options().SourceType, syncing, syncingConditionLastUpdate), nil
}

// SetSyncStatus implements the Parser interface
// SetSyncStatus sets the RepoSync sync status.
// `errs` includes the errors encountered during the apply step;
func (p *namespace) SetSyncStatus(ctx context.Context, newStatus *SyncStatus) error {
	p.mux.Lock()
	defer p.mux.Unlock()
	return p.setSyncStatusWithRetries(ctx, newStatus, defaultDenominator)
}

func (p *namespace) setSyncStatusWithRetries(ctx context.Context, newStatus *SyncStatus, denominator int) error {
	if denominator <= 0 {
		return fmt.Errorf("The denominator must be a positive number")
	}

	rs := &v1beta1.RepoSync{}
	if err := p.Client.Get(ctx, reposync.ObjectKey(p.Scope, p.SyncName), rs); err != nil {
		return status.APIServerError(err, fmt.Sprintf("failed to get the RepoSync object for the %v namespace", p.Scope))
	}

	currentRS := rs.DeepCopy()

	setSyncStatusFields(&rs.Status.Status, newStatus, denominator)

	// The Syncing condition should only represent the status and errors for the latest commit.
	// So only update the Syncing condition here if we haven't fetched a new commit.
	// Ideally, checking the source commit would be enough, but because fetching and parsing share the source status,
	// we also have to check the rendering commit, which may be updated first.
	var lastSyncStatus string
	if rs.Status.Source.Commit == rs.Status.Sync.Commit && rs.Status.Rendering.Commit == rs.Status.Sync.Commit {
		errorSources, errorSummary := summarizeErrorsForCommit(rs.Status.Source, rs.Status.Rendering, rs.Status.Sync, rs.Status.Sync.Commit)
		if newStatus.Syncing {
			reposync.SetSyncing(rs, true, "Sync", "Syncing", rs.Status.Sync.Commit, errorSources, errorSummary, rs.Status.Sync.LastUpdate)
		} else {
			if errorSummary.TotalCount == 0 {
				rs.Status.LastSyncedCommit = rs.Status.Sync.Commit
			}
			reposync.SetSyncing(rs, false, "Sync", "Sync Completed", rs.Status.Sync.Commit, errorSources, errorSummary, rs.Status.Sync.LastUpdate)
		}
		lastSyncStatus = metrics.StatusTagValueFromSummary(errorSummary)
	}

	// Avoid unnecessary status updates.
	if !currentRS.Status.Sync.LastUpdate.IsZero() && cmp.Equal(currentRS.Status, rs.Status, compare.IgnoreTimestampUpdates) {
		klog.V(5).Infof("Skipping status update for RepoSync %s/%s", rs.Namespace, rs.Name)
		return nil
	}

	csErrs := status.ToCSE(newStatus.Errs)
	metrics.RecordReconcilerErrors(ctx, "sync", csErrs)
	metrics.RecordPipelineError(ctx, configsync.RepoSyncName, "sync", len(csErrs))
	if len(csErrs) > 0 {
		klog.Infof("New sync errors for RepoSync %s/%s: %+v",
			rs.Namespace, rs.Name, csErrs)
	}
	// Only update the LastSyncTimestamp metric immediately after a sync attempt
	if !newStatus.Syncing && rs.Status.Sync.Commit != "" && lastSyncStatus != "" {
		metrics.RecordLastSync(ctx, lastSyncStatus, rs.Status.Sync.Commit, rs.Status.Sync.LastUpdate.Time)
	}

	if klog.V(5).Enabled() {
		klog.V(5).Infof("Updating sync status:\nDiff (- Removed, + Added):\n%s",
			cmp.Diff(currentRS.Status, rs.Status))
	}

	if err := p.Client.Status().Update(ctx, rs, client.FieldOwner(configsync.FieldManager)); err != nil {
		// If the update failure was caused by the size of the RepoSync object, we would truncate the errors and retry.
		if isRequestTooLargeError(err) {
			klog.Infof("Failed to update RepoSync sync status (total error count: %d, denominator: %d): %s.", rs.Status.Sync.ErrorSummary.TotalCount, denominator, err)
			return p.setSyncStatusWithRetries(ctx, newStatus, denominator*2)
		}
		return status.APIServerError(err, fmt.Sprintf("failed to update the RepoSync sync status for the %v namespace", p.Scope))
	}
	return nil
}

func (p *namespace) setLastPublishedMessage(ctx context.Context, messages map[pubsub.Status]pubsub.Message) error {
	//TODO add retry and truncation
	status := make(map[string]interface{})
	status["lastPublishedMessages"] = messages
	data := make(map[string]interface{})
	data["status"] = status
	patch, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding patch data: %v", err)
	}
	rs := &v1beta1.RepoSync{}
	rs.Namespace = string(p.Scope)
	rs.Name = p.SyncName
	if err = p.Client.Status().Patch(ctx, rs,
		client.RawPatch(types.MergePatchType, patch),
		client.FieldOwner(configsync.FieldManager),
	); err != nil {
		return fmt.Errorf("setting the lastPublishedMessage field in RepoSync '%s/%s'", rs.Namespace, rs.Name)
	}
	return nil
}

// SyncErrors returns all the sync errors, including remediator errors,
// validation errors, applier errors, and watch update errors.
// SyncErrors implements the Parser interface
func (p *namespace) SyncErrors() status.MultiError {
	return p.SyncErrorCache.Errors()
}

// K8sClient implements the Parser interface
func (p *namespace) K8sClient() client.Client {
	return p.Client
}
