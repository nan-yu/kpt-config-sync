// Copyright 2024 Google LLC
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
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/pubsub"
	"kpt.dev/configsync/pkg/status"
)

// ReconcilerStatus represents the status of the reconciler.
type ReconcilerStatus struct {
	// SourceStatus tracks info from the `Status.Source` field of a RepoSync/RootSync.
	SourceStatus *SourceStatus

	// RenderingStatus tracks info from the `Status.Rendering` field of a RepoSync/RootSync.
	RenderingStatus *RenderingStatus

	// SyncStatus tracks info from the `Status.Sync` field of a RepoSync/RootSync.
	SyncStatus *SyncStatus

	// SyncingConditionLastUpdate tracks when the `Syncing` condition was updated most recently.
	SyncingConditionLastUpdate metav1.Time

	// LastPublishedMessages tracks last published messages
	LastPublishedMessages map[pubsub.Status]pubsub.Message
}

// SetPublishedMessage updates the published message in the cache.
// It also clears the cache with opposite status.
func (s *ReconcilerStatus) SetPublishedMessage(msg pubsub.Message) {
	if s.LastPublishedMessages == nil {
		s.LastPublishedMessages = map[pubsub.Status]pubsub.Message{}
	}
	s.LastPublishedMessages[msg.Status] = msg

	switch msg.Status {
	case pubsub.ApplySucceeded:
		delete(s.LastPublishedMessages, pubsub.ApplyFailed)
	case pubsub.ApplyFailed:
		delete(s.LastPublishedMessages, pubsub.ApplySucceeded)
	case pubsub.ReconcileSucceeded:
		delete(s.LastPublishedMessages, pubsub.ReconcileFailed)
	case pubsub.ReconcileFailed:
		delete(s.LastPublishedMessages, pubsub.ReconcileSucceeded)
	}
}

// HasPubMessage checks if the message is already published.
func (s *ReconcilerStatus) HasPubMessage(msg pubsub.Message) bool {
	m, found := s.LastPublishedMessages[msg.Status]
	return found && reflect.DeepEqual(m, msg)
}

// DeepCopy returns a deep copy of the receiver.
// Warning: Go errors are not copy-able. So this isn't a true deep-copy.
func (s *ReconcilerStatus) DeepCopy() *ReconcilerStatus {
	return &ReconcilerStatus{
		SourceStatus:               s.SourceStatus.DeepCopy(),
		RenderingStatus:            s.RenderingStatus.DeepCopy(),
		SyncStatus:                 s.SyncStatus.DeepCopy(),
		SyncingConditionLastUpdate: *s.SyncingConditionLastUpdate.DeepCopy(),
	}
}

// needToSetSourceStatus returns true if `p.setSourceStatus` should be called.
func (s *ReconcilerStatus) needToSetSourceStatus(newStatus *SourceStatus) bool {
	if s.SourceStatus == nil {
		return newStatus != nil
	}
	// Update if not initialized
	if s.SourceStatus.LastUpdate.IsZero() {
		return true
	}
	// Update if source status was last updated before the rendering status
	if s.RenderingStatus != nil && s.SourceStatus.LastUpdate.Before(&s.RenderingStatus.LastUpdate) {
		return true
	}
	// Update if there's a diff
	return !s.SourceStatus.Equals(newStatus)
}

// needToSetSyncStatus returns true if `p.SetSyncStatus` should be called.
func (s *ReconcilerStatus) needToSetSyncStatus(newStatus *SyncStatus) bool {
	if s.SyncStatus == nil {
		return newStatus != nil
	}
	// Update if not initialized
	if s.SyncStatus.LastUpdate.IsZero() {
		return true
	}
	// Update if sync status was last updated before the rendering status
	if s.RenderingStatus != nil && s.SyncStatus.LastUpdate.Before(&s.RenderingStatus.LastUpdate) {
		return true
	}
	// Update if sync status was last updated before the source status
	if s.SourceStatus != nil && s.SyncStatus.LastUpdate.Before(&s.SourceStatus.LastUpdate) {
		return true
	}
	// Update if there's a diff
	return !s.SyncStatus.Equals(newStatus)
}

// SourceSpec is a representation of the source specification that is
// cached and stored in the RSync status for each stage in the pipeline.
//
// For the purposes of deciding when to skip updates, the SourceSpec is
// comparable for equality. If not equal, an update is necessary.
type SourceSpec interface {
	// Equals returns true if the specified SourceSpec equals this
	// SourceSpec, including type and all field values.
	Equals(SourceSpec) bool
}

// SourceSpecFromFileSource builds a SourceSpec from the FileSource.
// The type of SourceSpec depends on the SourceType.
// Commit is only necessary for Helm sources, because the chart Version is
// parsed from the "commit" string (`chart:version`).
func SourceSpecFromFileSource(source FileSource, sourceType configsync.SourceType, commit string) SourceSpec {
	var ss SourceSpec
	switch sourceType {
	case configsync.GitSource:
		ss = GitSourceSpec{
			Repo:     source.SourceRepo,
			Revision: source.SourceRev,
			Branch:   source.SourceBranch,
			Dir:      source.SyncDir.SlashPath(),
		}
	case configsync.OciSource:
		ss = OCISourceSpec{
			Image: source.SourceRepo,
			Dir:   source.SyncDir.SlashPath(),
		}
	case configsync.HelmSource:
		ss = HelmSourceSpec{
			Repo:    source.SourceRepo,
			Chart:   source.SyncDir.SlashPath(),
			Version: getChartVersionFromCommit(source.SourceRev, commit),
		}
	}
	return ss
}

// sourceRev will display the source version,
// but that could potentially be provided to use as a range of
// versions from which we pick the latest. We should display the
// version that was actually pulled down if we can.
// commit is expected to be of the format `chart:version`,
// so we parse it to grab the version.
func getChartVersionFromCommit(sourceRev, commit string) string {
	split := strings.Split(commit, ":")
	if len(split) == 2 {
		return split[1]
	}
	return sourceRev
}

// GitSourceSpec is a SourceSpec for the Git SourceType
type GitSourceSpec struct {
	Repo     string
	Revision string
	Branch   string
	Dir      string
}

// Equals returns true if the specified SourceSpec equals this
// GitSourceSpec, including type and all field values.
func (g GitSourceSpec) Equals(other SourceSpec) bool {
	t, ok := other.(GitSourceSpec)
	if !ok {
		return false
	}
	return t.Repo == g.Repo &&
		t.Revision == g.Revision &&
		t.Branch == g.Branch &&
		t.Dir == g.Dir
}

// OCISourceSpec is a SourceSpec for the OCI SourceType
type OCISourceSpec struct {
	Image string
	Dir   string
}

// Equals returns true if the specified SourceSpec equals this
// OCISourceSpec, including type and all field values.
func (o OCISourceSpec) Equals(other SourceSpec) bool {
	t, ok := other.(OCISourceSpec)
	if !ok {
		return false
	}
	return t.Image == o.Image &&
		t.Dir == o.Dir
}

// HelmSourceSpec is a SourceSpec for the Helm SourceType
type HelmSourceSpec struct {
	Repo    string
	Version string
	Chart   string
}

// Equals returns true if the specified SourceSpec equals this
// HelmSourceSpec, including type and all field values.
func (h HelmSourceSpec) Equals(other SourceSpec) bool {
	t, ok := other.(HelmSourceSpec)
	if !ok {
		return false
	}
	return t.Repo == h.Repo &&
		t.Version == h.Version &&
		t.Chart == h.Chart
}

// SourceStatus represents the status of the source stage of the pipeline.
type SourceStatus struct {
	// Spec represents the source specification that this status corresponds to.
	// The spec is stored in the status so we can distinguish if the status
	// reflects the latest spec or not.
	Spec       SourceSpec
	Commit     string
	Errs       status.MultiError
	LastUpdate metav1.Time
}

// DeepCopy returns a deep copy of the receiver.
// Warning: Go errors are not copy-able. So this isn't a true deep-copy.
func (gs *SourceStatus) DeepCopy() *SourceStatus {
	if gs == nil {
		return nil
	}
	return &SourceStatus{
		Commit:     gs.Commit,
		Errs:       gs.Errs,
		LastUpdate: *gs.LastUpdate.DeepCopy(),
	}
}

// Equals returns true if the specified SourceStatus equals this
// SourceStatus, excluding the LastUpdate timestamp.
func (gs *SourceStatus) Equals(other *SourceStatus) bool {
	if gs == nil {
		return other == nil
	}
	return gs.Commit == other.Commit &&
		status.DeepEqual(gs.Errs, other.Errs) &&
		isSourceSpecEqual(gs.Spec, other.Spec)
}

// RenderingStatus represents the status of the rendering stage of the pipeline.
type RenderingStatus struct {
	// Spec represents the source specification that this status corresponds to.
	// The spec is stored in the status so we can distinguish if the status
	// reflects the latest spec or not.
	Spec       SourceSpec
	Commit     string
	Message    string
	Errs       status.MultiError
	LastUpdate metav1.Time
	// RequiresRendering indicates whether the sync source has dry configs
	// only used internally (not surfaced on RSync status)
	RequiresRendering bool
}

// DeepCopy returns a deep copy of the receiver.
// Warning: Go errors are not copy-able. So this isn't a true deep-copy.
func (rs *RenderingStatus) DeepCopy() *RenderingStatus {
	if rs == nil {
		return nil
	}
	return &RenderingStatus{
		Commit:            rs.Commit,
		Message:           rs.Message,
		Errs:              rs.Errs,
		LastUpdate:        *rs.LastUpdate.DeepCopy(),
		RequiresRendering: rs.RequiresRendering,
	}
}

// Equals returns true if the specified RenderingStatus equals this
// RenderingStatus, excluding the LastUpdate timestamp.
func (rs *RenderingStatus) Equals(other *RenderingStatus) bool {
	if rs == nil {
		return other == nil
	}
	return rs.Commit == other.Commit &&
		rs.Message == other.Message &&
		status.DeepEqual(rs.Errs, other.Errs) &&
		isSourceSpecEqual(rs.Spec, other.Spec)
}

// SyncStatus represents the status of the sync stage of the pipeline.
type SyncStatus struct {
	// Spec represents the source specification that this status corresponds to.
	// The spec is stored in the status so we can distinguish if the status
	// reflects the latest spec or not.
	Spec       SourceSpec
	Syncing    bool
	Commit     string
	Errs       status.MultiError
	LastUpdate metav1.Time
}

// DeepCopy returns a deep copy of the receiver.
// Warning: Go errors are not copy-able. So this isn't a true deep-copy.
func (ss *SyncStatus) DeepCopy() *SyncStatus {
	if ss == nil {
		return nil
	}
	return &SyncStatus{
		Syncing:    ss.Syncing,
		Commit:     ss.Commit,
		Errs:       ss.Errs,
		LastUpdate: *ss.LastUpdate.DeepCopy(),
	}
}

// Equals returns true if the specified SyncStatus equals this
// SyncStatus, excluding the LastUpdate timestamp.
func (ss *SyncStatus) Equals(other *SyncStatus) bool {
	if ss == nil {
		return other == nil
	}
	return ss.Syncing == other.Syncing &&
		ss.Commit == other.Commit &&
		status.DeepEqual(ss.Errs, other.Errs) &&
		isSourceSpecEqual(ss.Spec, other.Spec)
}

// isSourceSpecEqual returns true if a & b are Equal, handling nil cases.
// None of the SourceSpec impls are nillable, but the interface itself is.
func isSourceSpecEqual(a, b SourceSpec) bool {
	switch {
	case a == nil:
		return b == nil
	case b == nil:
		return false
	default:
		return a.Equals(b)
	}
}
