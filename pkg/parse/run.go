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
	"fmt"
	"os"
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/hydrate"
	"kpt.dev/configsync/pkg/importer/filesystem/cmpath"
	"kpt.dev/configsync/pkg/metadata"
	"kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/util"
	webhookconfiguration "kpt.dev/configsync/pkg/webhook/configuration"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	triggerResync             = "resync"
	triggerReimport           = "reimport"
	triggerRetry              = "retry"
	triggerManagementConflict = "managementConflict"
	triggerWatchUpdate        = "watchUpdate"
	namespaceEvent            = "namespaceEvent"
)

const (
	// RenderingInProgress means that the configs are still being rendered by Config Sync.
	RenderingInProgress string = "Rendering is still in progress"

	// RenderingSucceeded means that the configs have been rendered successfully.
	RenderingSucceeded string = "Rendering succeeded"

	// RenderingFailed means that the configs have failed to be rendered.
	RenderingFailed string = "Rendering failed"

	// RenderingSkipped means that the configs don't need to be rendered.
	RenderingSkipped string = "Rendering skipped"

	// RenderingRequired means that the configs require rendering but the
	// hydration-controller is not currently running.
	RenderingRequired string = "Rendering required but is currently disabled"
	// RenderingNotRequired means that the configs do not require rendering but the
	// hydration-controller is currently running.
	RenderingNotRequired string = "Rendering not required but is currently enabled"
)

// RunResult encapsulates the result of a RunFunc.
// This simply allows explicitly naming return values in a way that makes the
// implementation easier to read.
type RunResult struct {
	SourceChanged bool
	Success       bool
	Errors        status.MultiError
}

// RunFunc is the function signature of the function that starts the parse-apply-watch loop
type RunFunc func(ctx context.Context, p Parser, trigger string, state *reconcilerState) RunResult

// DefaultRunFunc is the default implementation for RunOpts.RunFunc.
func DefaultRunFunc(ctx context.Context, p Parser, trigger string, state *reconcilerState) RunResult {
	result := RunResult{}
	// Initialize status
	// TODO: Populate status from RSync status
	if state.status == nil {
		reconcilerStatus, err := p.ReconcilerStatusFromCluster(ctx)
		if err != nil {
			errors := status.Append(nil, err)
			state.invalidate(errors)
			result.Errors = errors
			return result
		}
		state.status = reconcilerStatus
	}
	opts := p.options()
	var syncDir cmpath.Absolute
	gs := &SourceStatus{}
	// pull the source commit and directory with retries within 5 minutes.
	gs.Commit, syncDir, gs.Errs = hydrate.SourceCommitAndDirWithRetry(util.SourceRetryBackoff, opts.SourceType, opts.SourceDir, opts.SyncDir, opts.ReconcilerName)

	// Generate source spec from Reconciler config
	gs.Spec = SourceSpecFromFileSource(opts.FileSource, opts.SourceType, gs.Commit)

	// Only update the source status if there are errors or the commit changed.
	// Otherwise, parsing errors may be overwritten.
	// TODO: Decouple fetch & parse stages to use different status fields
	if gs.Errs != nil || state.status.SourceStatus == nil || gs.Commit != state.status.SourceStatus.Commit {
		gs.LastUpdate = metav1.Time{Time: opts.Clock.Now()}
		var setSourceStatusErr error
		// Only update the source status if it changed
		if state.status.needToSetSourceStatus(gs) {
			klog.V(3).Info("Updating source status (after fetch)")
			setSourceStatusErr = p.setSourceStatus(ctx, gs)
			// If there were errors publishing the source status, stop, log them, and retry later
			if setSourceStatusErr != nil {
				// If there were fetch errors, log those too
				errors := status.Append(gs.Errs, setSourceStatusErr)
				state.invalidate(errors)
				result.Errors = errors
				return result
			}
			// Cache the latest source status in memory
			state.status.SourceStatus = gs
			state.status.SyncingConditionLastUpdate = gs.LastUpdate
		}
		// If there were fetch errors, stop, log them, and retry later
		if gs.Errs != nil {
			state.invalidate(gs.Errs)
			result.Errors = gs.Errs
			return result
		}
	}

	rs := &RenderingStatus{
		Spec:   gs.Spec,
		Commit: gs.Commit,
	}
	if state.status.RenderingStatus != nil {
		rs.RequiresRendering = state.status.RenderingStatus.RequiresRendering
	}

	// set the rendering status by checking the done file.
	if opts.RenderingEnabled {
		doneFilePath := opts.RepoRoot.Join(cmpath.RelativeSlash(hydrate.DoneFile)).OSPath()
		_, err := os.Stat(doneFilePath)
		if os.IsNotExist(err) || (err == nil && hydrate.DoneCommit(doneFilePath) != gs.Commit) {
			rs.Message = RenderingInProgress
			rs.LastUpdate = metav1.Time{Time: opts.Clock.Now()}
			klog.V(3).Info("Updating rendering status (before parse)")
			setRenderingStatusErr := p.setRenderingStatus(ctx, state.status.RenderingStatus, rs)
			if setRenderingStatusErr == nil {
				state.reset()
				state.status.RenderingStatus = rs
				state.status.SyncingConditionLastUpdate = rs.LastUpdate
			} else {
				errors := status.Append(nil, setRenderingStatusErr)
				state.invalidate(errors)
				result.Errors = errors
			}
			return result
		}
		if err != nil {
			rs.Message = RenderingFailed
			rs.LastUpdate = metav1.Time{Time: opts.Clock.Now()}
			rs.Errs = status.InternalHydrationError(err, "unable to read the done file: %s", doneFilePath)
			klog.V(3).Info("Updating rendering status (before parse)")
			setRenderingStatusErr := p.setRenderingStatus(ctx, state.status.RenderingStatus, rs)
			if setRenderingStatusErr == nil {
				state.status.RenderingStatus = rs
				state.status.SyncingConditionLastUpdate = rs.LastUpdate
			}
			errors := status.Append(rs.Errs, setRenderingStatusErr)
			state.invalidate(errors)
			result.Errors = errors
			return result
		}
	}

	// Init cached source
	if state.cache.source == nil {
		state.cache.source = &sourceState{}
	}

	// rendering is done, starts to read the source or hydrated configs.
	oldSyncDir := state.cache.source.syncDir
	// `read` is called no matter what the trigger is.
	ps := &sourceState{
		spec:    gs.Spec,
		commit:  gs.Commit,
		syncDir: syncDir,
	}
	if errs := read(ctx, p, trigger, state, ps); errs != nil {
		state.invalidate(errs)
		result.Errors = errs
		return result
	}

	newSyncDir := state.cache.source.syncDir

	if newSyncDir != oldSyncDir {
		// If the commit changed and parsing succeeded, trigger retries to start again, if stopped.
		result.SourceChanged = true
	}

	// The parse-apply-watch sequence will be skipped if the trigger type is `triggerReimport` and
	// there is no new source changes. The reasons are:
	//   * If a former parse-apply-watch sequence for syncDir succeeded, there is no need to run the sequence again;
	//   * If all the former parse-apply-watch sequences for syncDir failed, the next retry will call the sequence.
	if trigger == triggerReimport && oldSyncDir == newSyncDir {
		return result
	}

	errs := parseAndUpdate(ctx, p, trigger, state)
	if errs != nil {
		state.invalidate(errs)
		result.Errors = errs
		return result
	}

	// Only checkpoint the state after *everything* succeeded, including status update.
	state.checkpoint()
	result.Success = true
	return result
}

// read reads config files from source if no rendering is needed, or from hydrated output if rendering is done.
// It also updates the .status.rendering and .status.source fields.
func read(ctx context.Context, p Parser, trigger string, state *reconcilerState, sourceState *sourceState) status.MultiError {
	opts := p.options()
	hydrationStatus, sourceStatus := readFromSource(ctx, p, trigger, state, sourceState)
	if opts.RenderingEnabled != hydrationStatus.RequiresRendering {
		// the reconciler is misconfigured. set the annotation so that the reconciler-manager
		// will recreate this reconciler with the correct configuration.
		if err := p.setRequiresRendering(ctx, hydrationStatus.RequiresRendering); err != nil {
			hydrationStatus.Errs = status.Append(hydrationStatus.Errs,
				status.InternalHydrationError(err, "error setting %s annotation", metadata.RequiresRenderingAnnotationKey))
		}
	}
	hydrationStatus.LastUpdate = metav1.Time{Time: opts.Clock.Now()}
	// update the rendering status before source status because the parser needs to
	// read and parse the configs after rendering is done and there might have errors.
	klog.V(3).Info("Updating rendering status (after parse)")
	setRenderingStatusErr := p.setRenderingStatus(ctx, state.status.RenderingStatus, hydrationStatus)
	if setRenderingStatusErr == nil {
		state.status.RenderingStatus = hydrationStatus
		state.status.SyncingConditionLastUpdate = hydrationStatus.LastUpdate
	}
	renderingErrs := status.Append(hydrationStatus.Errs, setRenderingStatusErr)
	if renderingErrs != nil {
		return renderingErrs
	}

	if sourceStatus.Errs == nil {
		return nil
	}

	// Only call `setSourceStatus` if `readFromSource` fails.
	// If `readFromSource` succeeds, `parse` may still fail.
	sourceStatus.LastUpdate = metav1.Time{Time: opts.Clock.Now()}
	var setSourceStatusErr error
	if state.status.needToSetSourceStatus(sourceStatus) {
		klog.V(3).Info("Updating source status (after parse)")
		setSourceStatusErr := p.setSourceStatus(ctx, sourceStatus)
		if setSourceStatusErr == nil {
			state.status.SourceStatus = sourceStatus
			state.status.SyncingConditionLastUpdate = sourceStatus.LastUpdate
		}
	}

	return status.Append(sourceStatus.Errs, setSourceStatusErr)
}

// parseHydrationState reads from the file path which the hydration-controller
// container writes to. It checks if the hydrated files are ready and returns
// a renderingStatus.
func parseHydrationState(p Parser, srcState *sourceState, hydrationStatus *RenderingStatus) (*sourceState, *RenderingStatus) {
	opts := p.options()
	if !opts.RenderingEnabled {
		hydrationStatus.Message = RenderingSkipped
		return srcState, hydrationStatus
	}
	// Check if the hydratedRoot directory exists.
	// If exists, read the hydrated directory. Otherwise, fail.
	absHydratedRoot, err := cmpath.AbsoluteOS(opts.HydratedRoot)
	if err != nil {
		hydrationStatus.Message = RenderingFailed
		hydrationStatus.Errs = status.InternalHydrationError(err, "hydrated-dir must be an absolute path")
		return srcState, hydrationStatus
	}

	var hydrationErr hydrate.HydrationError
	if _, err := os.Stat(absHydratedRoot.OSPath()); err == nil {
		// pull the hydrated commit and directory with retries within 1 minute.
		srcState, hydrationErr = opts.readHydratedDirWithRetry(util.HydratedRetryBackoff, absHydratedRoot, opts.ReconcilerName, srcState)
		if hydrationErr != nil {
			hydrationStatus.Message = RenderingFailed
			hydrationStatus.Errs = status.HydrationError(hydrationErr.Code(), hydrationErr)
			return srcState, hydrationStatus
		}
		hydrationStatus.Message = RenderingSucceeded
	} else if !os.IsNotExist(err) {
		hydrationStatus.Message = RenderingFailed
		hydrationStatus.Errs = status.InternalHydrationError(err, "unable to evaluate the hydrated path %s", absHydratedRoot.OSPath())
		return srcState, hydrationStatus
	} else {
		// Source of truth does not require hydration, but hydration-controller is running
		hydrationStatus.Message = RenderingNotRequired
		hydrationStatus.RequiresRendering = false
		err := hydrate.NewTransientError(fmt.Errorf("sync source contains only wet configs and hydration-controller is running"))
		hydrationStatus.Errs = status.HydrationError(err.Code(), err)
		return srcState, hydrationStatus
	}
	return srcState, hydrationStatus
}

// readFromSource reads the source or hydrated configs, checks whether the sourceState in
// the cache is up-to-date. If the cache is not up-to-date, reads all the source or hydrated files.
// readFromSource returns the rendering status and source status.
func readFromSource(ctx context.Context, p Parser, trigger string, recState *reconcilerState, srcState *sourceState) (*RenderingStatus, *SourceStatus) {
	opts := p.options()
	start := opts.Clock.Now()

	hydrationStatus := &RenderingStatus{
		Spec:              srcState.spec,
		Commit:            srcState.commit,
		RequiresRendering: opts.RenderingEnabled,
	}
	srcStatus := &SourceStatus{
		Spec:   srcState.spec,
		Commit: srcState.commit,
	}

	srcState, hydrationStatus = parseHydrationState(p, srcState, hydrationStatus)
	if hydrationStatus.Errs != nil {
		return hydrationStatus, srcStatus
	}

	if srcState.syncDir == recState.cache.source.syncDir {
		return hydrationStatus, srcStatus
	}

	// Read all the files under srcState.syncDir
	srcStatus.Errs = opts.readConfigFiles(srcState)

	if !opts.RenderingEnabled {
		// Check if any kustomization Files exist
		for _, fi := range srcState.files {
			if hydrate.HasKustomization(path.Base(fi.OSPath())) {
				// Source of truth requires hydration, but the hydration-controller is not running
				hydrationStatus.Message = RenderingRequired
				hydrationStatus.RequiresRendering = true
				err := hydrate.NewTransientError(fmt.Errorf("sync source contains dry configs and hydration-controller is not running"))
				hydrationStatus.Errs = status.HydrationError(err.Code(), err)
				return hydrationStatus, srcStatus
			}
		}
	}

	klog.Infof("New source changes (%s) detected, reset the cache", srcState.syncDir.OSPath())
	// Reset the cache to make sure all the steps of a parse-apply-watch loop will run.
	recState.resetCache()
	if srcStatus.Errs == nil {
		// Set `state.cache.source` after `readConfigFiles` succeeded
		recState.cache.source = srcState
	}
	metrics.RecordParserDuration(ctx, trigger, "read", metrics.StatusTagKey(srcStatus.Errs), start)
	return hydrationStatus, srcStatus
}

func parseSource(ctx context.Context, p Parser, trigger string, state *reconcilerState) status.MultiError {
	if state.cache.parserResultUpToDate() {
		return nil
	}

	opts := p.options()
	start := opts.Clock.Now()
	var sourceErrs status.MultiError

	objs, errs := p.parseSource(ctx, state.cache.source)
	if !opts.WebhookEnabled {
		klog.V(3).Infof("Removing %s annotation as Admission Webhook is disabled", metadata.DeclaredFieldsKey)
		for _, obj := range objs {
			core.RemoveAnnotations(obj, metadata.DeclaredFieldsKey)
		}
	}
	sourceErrs = status.Append(sourceErrs, errs)
	metrics.RecordParserDuration(ctx, trigger, "parse", metrics.StatusTagKey(sourceErrs), start)
	state.cache.setParserResult(objs, sourceErrs)

	if !status.HasBlockingErrors(sourceErrs) && opts.WebhookEnabled {
		err := webhookconfiguration.Update(ctx, opts.k8sClient(), opts.discoveryClient(), objs,
			client.FieldOwner(configsync.FieldManager))
		if err != nil {
			// Don't block if updating the admission webhook fails.
			// Return an error instead if we remove the remediator as otherwise we
			// will simply never correct the type.
			// This should be treated as a warning once we have
			// that capability.
			klog.Errorf("Failed to update admission webhook: %v", err)
			// TODO: Handle case where multiple reconciler Pods try to
			//  create or update the Configuration simultaneously.
		}
	}

	return sourceErrs
}

func parseAndUpdate(ctx context.Context, p Parser, trigger string, state *reconcilerState) status.MultiError {
	opts := p.options()
	klog.V(3).Info("Parser starting...")
	sourceErrs := parseSource(ctx, p, trigger, state)
	klog.V(3).Info("Parser stopped")
	newSourceStatus := &SourceStatus{
		Spec:       state.cache.source.spec,
		Commit:     state.cache.source.commit,
		Errs:       sourceErrs,
		LastUpdate: metav1.Time{Time: opts.Clock.Now()},
	}
	if state.status.needToSetSourceStatus(newSourceStatus) {
		klog.V(3).Info("Updating source status (after parse)")
		if err := p.setSourceStatus(ctx, newSourceStatus); err != nil {
			// If `p.setSourceStatus` fails, we terminate the reconciliation.
			// If we call `update` in this case and `update` succeeds, `Status.Source.Commit` would end up be older
			// than `Status.Sync.Commit`.
			return status.Append(sourceErrs, err)
		}
		state.status.SourceStatus = newSourceStatus
		state.status.SyncingConditionLastUpdate = newSourceStatus.LastUpdate
	}

	if status.HasBlockingErrors(sourceErrs) {
		return sourceErrs
	}

	// Create a new context with its cancellation function.
	ctxForUpdateSyncStatus, cancel := context.WithCancel(context.Background())

	go updateSyncStatusPeriodically(ctxForUpdateSyncStatus, p, state)

	klog.V(3).Info("Updater starting...")
	start := opts.Clock.Now()
	updateErrs := opts.Update(ctx, &state.cache)
	metrics.RecordParserDuration(ctx, trigger, "update", metrics.StatusTagKey(updateErrs), start)
	klog.V(3).Info("Updater stopped")

	// This is to terminate `updateSyncStatusPeriodically`.
	cancel()
	// TODO: Wait for periodic updates to stop

	// SyncErrors include errors from both the Updater and Remediator
	klog.V(3).Info("Updating sync status (after sync)")
	syncErrs := p.SyncErrors()
	if err := setSyncStatus(ctx, p, state, state.status.SourceStatus.Spec, false, state.cache.source.commit, syncErrs); err != nil {
		syncErrs = status.Append(syncErrs, err)
	}

	// Return all the errors from the Parser, Updater, and Remediator
	return status.Append(sourceErrs, syncErrs)
}

// setSyncStatus updates `.status.sync` and the Syncing condition, if needed,
// as well as `state.syncStatus` and `state.syncingConditionLastUpdate` if
// the update is successful.
func setSyncStatus(ctx context.Context, p Parser, state *reconcilerState, spec SourceSpec, syncing bool, commit string, syncErrs status.MultiError) error {
	options := p.options()
	// Update the RSync status, if necessary
	newSyncStatus := &SyncStatus{
		Spec:       spec,
		Syncing:    syncing,
		Commit:     commit,
		Errs:       syncErrs,
		LastUpdate: metav1.Time{Time: options.Clock.Now()},
	}
	if state.status.needToSetSyncStatus(newSyncStatus) {
		if err := p.SetSyncStatus(ctx, newSyncStatus); err != nil {
			return err
		}
		state.status.SyncStatus = newSyncStatus
		state.status.SyncingConditionLastUpdate = newSyncStatus.LastUpdate
	}

	// Report conflict errors to the remote manager, if it's a RootSync.
	if err := reportRootSyncConflicts(ctx, p.K8sClient(), options.ManagementConflicts()); err != nil {
		return fmt.Errorf("failed to report remote conflicts: %w", err)
	}
	return nil
}

// updateSyncStatusPeriodically update the sync status periodically until the
// cancellation function of the context is called.
func updateSyncStatusPeriodically(ctx context.Context, p Parser, state *reconcilerState) {
	opts := p.options()
	klog.V(3).Info("Periodic sync status updates starting...")
	updatePeriod := opts.StatusUpdatePeriod
	updateTimer := opts.Clock.NewTimer(updatePeriod)
	defer updateTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			// ctx.Done() is closed when the cancellation function of the context is called.
			klog.V(3).Info("Periodic sync status updates stopped")
			return

		case <-updateTimer.C():
			klog.V(3).Info("Updating sync status (periodic while syncing)")
			if err := setSyncStatus(ctx, p, state, state.status.SourceStatus.Spec, true, state.cache.source.commit, p.SyncErrors()); err != nil {
				klog.Warningf("failed to update sync status: %v", err)
			}

			updateTimer.Reset(updatePeriod) // Schedule status update attempt
		}
	}
}

// reportRootSyncConflicts reports conflicts to the RootSync that manages the
// conflicting resources.
func reportRootSyncConflicts(ctx context.Context, k8sClient client.Client, conflictErrs []status.ManagementConflictError) error {
	if len(conflictErrs) == 0 {
		return nil
	}
	conflictingManagerErrors := map[string][]status.ManagementConflictError{}
	for _, conflictError := range conflictErrs {
		conflictingManager := conflictError.CurrentManager()
		conflictingManagerErrors[conflictingManager] = append(conflictingManagerErrors[conflictingManager], conflictError)
	}

	for conflictingManager, conflictErrors := range conflictingManagerErrors {
		scope, name := declared.ManagerScopeAndName(conflictingManager)
		if scope == declared.RootScope {
			// RootSync applier uses PolicyAdoptAll.
			// So it may fight, if the webhook is disabled.
			// Report the conflict to the other RootSync to make it easier to detect.
			klog.Infof("Detected conflict with RootSync manager %q", conflictingManager)
			if err := prependRootSyncRemediatorStatus(ctx, k8sClient, name, conflictErrors, defaultDenominator); err != nil {
				return fmt.Errorf("failed to update RootSync %q to prepend remediator conflicts: %w", name, err)
			}
		} else {
			// RepoSync applier uses PolicyAdoptIfNoInventory.
			// So it won't fight, even if the webhook is disabled.
			klog.Infof("Detected conflict with RepoSync manager %q", conflictingManager)
		}
	}
	return nil
}
