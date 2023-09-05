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
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/utils/pointer"
	"kpt.dev/configsync/pkg/api/configmanagement"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/applier"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/diff/difftest"
	"kpt.dev/configsync/pkg/importer/analyzer/ast"
	"kpt.dev/configsync/pkg/importer/filesystem"
	"kpt.dev/configsync/pkg/importer/reader"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/metadata"
	"kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/status"
	syncertest "kpt.dev/configsync/pkg/syncer/syncertest/fake"
	"kpt.dev/configsync/pkg/testing/fake"
	"kpt.dev/configsync/pkg/testing/testmetrics"
	discoveryutil "kpt.dev/configsync/pkg/util/discovery"
	"sigs.k8s.io/cli-utils/pkg/testutil"

	"sigs.k8s.io/cli-utils/pkg/common"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	rootSyncName       = "my-rs"
	rootReconcilerName = "root-reconciler-my-rs"
	nilGitContext      = `{"repo":""}`
)

type noOpRemediator struct {
	needsUpdate bool
}

func (r *noOpRemediator) Pause() {}

func (r *noOpRemediator) Resume() {}

func (r *noOpRemediator) ConflictErrors() []status.ManagementConflictError {
	return nil
}

func (r *noOpRemediator) FightErrors() []status.Error {
	return nil
}

func (r *noOpRemediator) NeedsUpdate() bool {
	return r.needsUpdate
}

func (r *noOpRemediator) ManagementConflict() bool {
	return false
}

func (r *noOpRemediator) UpdateWatches(_ context.Context, _ map[schema.GroupVersionKind]struct{}) status.MultiError {
	r.needsUpdate = false
	return nil
}

func (r *noOpRemediator) Errors() status.MultiError {
	return nil
}

func gitSpec(repo string, auth configsync.AuthType) core.MetaMutator {
	return func(o client.Object) {
		if rs, ok := o.(*v1beta1.RootSync); ok {
			rs.Spec.Git = &v1beta1.Git{
				Repo: repo,
				Auth: auth,
			}
		}
	}
}

func TestRoot_Parse(t *testing.T) {
	testCases := []struct {
		name            string
		format          filesystem.SourceFormat
		existingObjects []client.Object
		parsed          []ast.FileObject
		want            []ast.FileObject
	}{
		{
			name:   "no objects",
			format: filesystem.SourceFormatUnstructured,
		},
		{
			name:   "implicit namespace if unstructured and not present",
			format: filesystem.SourceFormatUnstructured,
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
			},
			want: []ast.FileObject{
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:   "implicit namespace if unstructured, present and self-managed",
			format: filesystem.SourceFormatUnstructured,
			existingObjects: []client.Object{fake.NamespaceObject("foo",
				core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
				core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
				core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
				core.Annotation(metadata.GitContextKey, nilGitContext),
				core.Annotation(metadata.SyncTokenAnnotationKey, ""),
				core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
				core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
				difftest.ManagedBy(declared.RootReconciler, rootSyncName))},
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
			},
			want: []ast.FileObject{
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:   "no implicit namespace if unstructured, present, but managed by others",
			format: filesystem.SourceFormatUnstructured,
			existingObjects: []client.Object{fake.NamespaceObject("foo",
				core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
				core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
				core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
				core.Annotation(metadata.GitContextKey, nilGitContext),
				core.Annotation(metadata.SyncTokenAnnotationKey, ""),
				core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
				core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
				difftest.ManagedBy(declared.RootReconciler, "other-root-sync"))},
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
			},
			want: []ast.FileObject{
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:            "no implicit namespace if unstructured, present, but unmanaged",
			format:          filesystem.SourceFormatUnstructured,
			existingObjects: []client.Object{fake.NamespaceObject("foo")},
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
			},
			want: []ast.FileObject{
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:   "no implicit namespace if unstructured and namespace is config-management-system",
			format: filesystem.SourceFormatUnstructured,
			parsed: []ast.FileObject{
				fake.RootSyncV1Beta1("test", fake.WithRootSyncSourceType(v1beta1.GitSource), gitSpec("https://github.com/test/test.git", configsync.AuthNone)),
			},
			want: []ast.FileObject{
				fake.RootSyncV1Beta1("test", gitSpec("https://github.com/test/test.git", configsync.AuthNone),
					fake.WithRootSyncSourceType(v1beta1.GitSource),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1beta1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/spec/git/auth, /spec/git/period, /spec/git/repo, /spec/sourceType, /status/rendering/lastUpdate, /status/source/lastUpdate, /status/sync/lastUpdate"),
					core.Annotation(metadata.SourcePathAnnotationKey, fmt.Sprintf("namespaces/%s/test.yaml", configsync.ControllerNamespace)),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "configsync.gke.io_rootsync_config-management-system_test"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:   "multiple objects share a single implicit namespace",
			format: filesystem.SourceFormatUnstructured,
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("bar")),
				fake.ConfigMap(core.Namespace("bar")),
			},
			want: []ast.FileObject{
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_bar"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_bar_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.ConfigMap(core.Namespace("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, ""),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/configmap.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_configmap_bar_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			name:   "multiple implicit namespaces",
			format: filesystem.SourceFormatUnstructured,
			existingObjects: []client.Object{
				fake.NamespaceObject("foo"), // foo exists but not managed, should NOT be added as an implicit namespace
				// bar not exists, should be added as an implicit namespace
				fake.NamespaceObject("baz", // baz exists and self-managed, should be added as an implicit namespace
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_baz"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName)),
			},
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
				fake.Role(core.Namespace("bar")),
				fake.ConfigMap(core.Namespace("bar")),
				fake.Role(core.Namespace("baz")),
				fake.ConfigMap(core.Namespace("baz")),
			},
			want: []ast.FileObject{
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_bar"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_bar_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.ConfigMap(core.Namespace("bar"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, ""),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/configmap.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_configmap_bar_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("baz"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_baz"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("baz"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_baz_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.ConfigMap(core.Namespace("baz"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, ""),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/configmap.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_configmap_baz_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parser := &root{
				sourceFormat: tc.format,
				opts: opts{
					parser:             &fakeParser{parse: tc.parsed},
					syncName:           rootSyncName,
					reconcilerName:     rootReconcilerName,
					client:             syncertest.NewClient(t, core.Scheme, fake.RootSyncObjectV1Beta1(rootSyncName)),
					discoveryInterface: syncertest.NewDiscoveryClient(kinds.Namespace(), kinds.Role()),
					updater: updater{
						scope:      declared.RootReconciler,
						resources:  &declared.Resources{},
						remediator: &noOpRemediator{},
						applier:    &fakeApplier{},
					},
					mux: &sync.Mutex{},
				},
			}
			for _, o := range tc.existingObjects {
				if err := parser.client.Create(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			}
			state := reconcilerState{}
			if err := parseAndUpdate(context.Background(), parser, triggerReimport, &state); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.want, state.cache.objsToApply, cmpopts.EquateEmpty(), ast.CompareFileObject, cmpopts.SortSlices(sortObjects)); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func fakeCRD(opts ...core.MetaMutator) ast.FileObject {
	crd := fake.CustomResourceDefinitionV1Object(opts...)
	crd.Spec.Group = "acme.com"
	crd.Spec.Names = apiextensionsv1.CustomResourceDefinitionNames{
		Plural:   "anvils",
		Singular: "anvil",
		Kind:     "Anvil",
	}
	crd.Spec.Scope = apiextensionsv1.NamespaceScoped
	crd.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  true,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"lbs"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"lbs": {
									Type:    "integer",
									Minimum: pointer.Float64(1.0),
									Maximum: pointer.Float64(9000.0),
								},
							},
						},
					},
				},
			},
		},
	}
	return fake.FileObject(crd, "cluster/crd.yaml")
}

func fakeFileObjects() []ast.FileObject {
	var fileObjects []ast.FileObject
	for i := 0; i < 500; i++ {
		fileObjects = append(fileObjects, fake.RoleAtPath(fmt.Sprintf("namespaces/foo/role%v.yaml", i), core.Namespace("foo")))
	}
	return fileObjects
}

func fakeGVKs() []schema.GroupVersionKind {
	var gvks []schema.GroupVersionKind
	for i := 0; i < 500; i++ {
		gvks = append(gvks, schema.GroupVersionKind{Group: fmt.Sprintf("acme%v.com", i), Version: "v1", Kind: "APIService"})
	}
	return gvks
}

func fakeParseError(err error, gvks ...schema.GroupVersionKind) error {
	groups := make(map[schema.GroupVersion]error)
	for _, gvk := range gvks {
		gv := gvk.GroupVersion()
		groups[gv] = err
	}
	return status.APIServerError(&discovery.ErrGroupDiscoveryFailed{Groups: groups}, "API discovery failed")
}

func TestRoot_Parse_Discovery(t *testing.T) {
	testCases := []struct {
		name            string
		parsed          []ast.FileObject
		want            []ast.FileObject
		discoveryClient discoveryutil.ServerResourcer
		expectedError   error
	}{
		{
			// unknown scoped object should not be skipped when sending to applier when discovery call fails
			name:            "unknown scoped object with discovery failure of deadline exceeded error",
			discoveryClient: syncertest.NewDiscoveryClientWithError(context.DeadlineExceeded, kinds.Namespace(), kinds.Role()),
			expectedError:   fakeParseError(context.DeadlineExceeded, kinds.Namespace(), kinds.Role()),
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
				// add a faked obect in parser.parsed without CRD so it's scope will be unknown when validating
				fake.Unstructured(kinds.Anvil(), core.Name("deploy")),
			},
			want: []ast.FileObject{},
		},
		{
			// unknown scoped object should not be skipped when sending to applier when discovery call fails
			name:            "unknown scoped object with discovery failure of http request canceled failure",
			discoveryClient: syncertest.NewDiscoveryClientWithError(errors.New("net/http: request canceled (Client.Timeout exceeded while awaiting headers)"), kinds.Namespace(), kinds.Role()),
			expectedError:   fakeParseError(errors.New("net/http: request canceled (Client.Timeout exceeded while awaiting headers)"), kinds.Namespace(), kinds.Role()),
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
				// add a faked obect in parser.parsed without CRD so it's scope will be unknown when validating
				fake.Unstructured(kinds.Anvil(), core.Name("deploy")),
			},
			want: []ast.FileObject{},
		},
		{
			// unknown scoped object should not be skipped when sending to applier when discovery call fails
			name:            "unknown scoped object with discovery failure of 500 deadline exceeded failure",
			discoveryClient: syncertest.NewDiscoveryClientWithError(context.DeadlineExceeded, fakeGVKs()...),
			expectedError:   fakeParseError(context.DeadlineExceeded, fakeGVKs()...),
			parsed:          append(fakeFileObjects(), fake.Unstructured(kinds.Anvil(), core.Name("deploy"))),
			want:            []ast.FileObject{},
		},
		{
			// unknown scoped object get skipped when sending to applier when discovery call is good
			name:            "unknown scoped object without discovery failure",
			discoveryClient: syncertest.NewDiscoveryClientWithError(nil, kinds.Namespace(), kinds.Role()),
			expectedError:   status.UnknownObjectKindError(fake.Unstructured(kinds.Anvil(), core.Name("deploy"))),
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
				// add a faked obect in parser.parsed without CRD so it's scope will be unknown when validating
				fake.Unstructured(kinds.Anvil(), core.Name("deploy")),
			},
			want: []ast.FileObject{
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
			},
		},
		{
			// happy path condition
			name:            "known scoped object without discovery failure",
			discoveryClient: syncertest.NewDiscoveryClient(kinds.Namespace(), kinds.Role()),
			expectedError:   nil,
			parsed: []ast.FileObject{
				fake.Role(core.Namespace("foo")),
				fakeCRD(core.Name("anvils.acme.com")),
				fake.Unstructured(kinds.Anvil(), core.Name("deploy"), core.Namespace("foo")),
			},
			want: []ast.FileObject{
				fake.UnstructuredAtPath(kinds.Namespace(),
					"",
					core.Name("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Annotation(common.LifecycleDeleteAnnotation, common.PreventDeletion),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "_namespace_foo"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fake.Role(core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/rules"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/foo/role.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_role_foo_default-name"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName),
				),
				fakeCRD(core.Name("anvils.acme.com"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, "/spec/group, /spec/names/kind, /spec/names/plural, /spec/names/singular, /spec/scope, /spec/versions, /status/acceptedNames/kind, /status/acceptedNames/plural, /status/conditions, /status/storedVersions"),
					core.Annotation(metadata.SourcePathAnnotationKey, "cluster/crd.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "apiextensions.k8s.io_customresourcedefinition_anvils.acme.com"),
					difftest.ManagedBy(declared.RootReconciler, rootSyncName)),
				fake.Unstructured(kinds.Anvil(),
					core.Name("deploy"),
					core.Namespace("foo"),
					core.Label(metadata.ManagedByKey, metadata.ManagedByValue),
					core.Label(metadata.DeclaredVersionLabel, "v1"),
					core.Annotation(metadata.DeclaredFieldsKey, ""),
					core.Annotation(metadata.ResourceManagerKey, ":root_my-rs"),
					core.Annotation(metadata.SourcePathAnnotationKey, "namespaces/obj.yaml"),
					core.Annotation(metadata.ResourceManagementKey, metadata.ResourceManagementEnabled),
					core.Annotation(metadata.GitContextKey, nilGitContext),
					core.Annotation(metadata.SyncTokenAnnotationKey, ""),
					core.Annotation(metadata.OwningInventoryKey, applier.InventoryID(rootSyncName, configmanagement.ControllerNamespace)),
					core.Annotation(metadata.ResourceIDKey, "acme.com_anvil_foo_deploy"),
				),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parser := &root{
				sourceFormat: filesystem.SourceFormatUnstructured,
				opts: opts{
					parser:             &fakeParser{parse: tc.parsed},
					syncName:           rootSyncName,
					reconcilerName:     rootReconcilerName,
					client:             syncertest.NewClient(t, core.Scheme, fake.RootSyncObjectV1Beta1(rootSyncName)),
					discoveryInterface: tc.discoveryClient,
					updater: updater{
						scope:      declared.RootReconciler,
						resources:  &declared.Resources{},
						remediator: &noOpRemediator{},
						applier:    &fakeApplier{},
					},
					mux: &sync.Mutex{},
				},
			}
			state := reconcilerState{}
			err := parseAndUpdate(context.Background(), parser, triggerReimport, &state)
			testutil.AssertEqual(t, tc.expectedError, err, "expected error to match")

			if diff := cmp.Diff(tc.want, state.cache.objsToApply, cmpopts.EquateEmpty(), ast.CompareFileObject, cmpopts.SortSlices(sortObjects)); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestRoot_ParseErrorsMetricValidation(t *testing.T) {
	testCases := []struct {
		name        string
		errors      []status.Error
		wantMetrics []*view.Row
	}{
		{
			name: "single parse error",
			errors: []status.Error{
				status.InternalError("internal error"),
			},
			wantMetrics: []*view.Row{
				{Data: &view.CountData{Value: 1}, Tags: []tag.Tag{{}}},
			},
		},
		{
			name: "multiple parse errors",
			errors: []status.Error{
				status.InternalError("internal error"),
				status.SourceError.Sprintf("source error").Build(),
				status.InternalError("another internal error"),
			},
			wantMetrics: []*view.Row{
				{Data: &view.CountData{Value: 2}, Tags: []tag.Tag{{}}},
				{Data: &view.CountData{Value: 1}, Tags: []tag.Tag{{}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parser := &root{
				sourceFormat: filesystem.SourceFormatUnstructured,
				opts: opts{
					parser:             &fakeParser{errors: tc.errors},
					syncName:           rootSyncName,
					reconcilerName:     rootReconcilerName,
					client:             syncertest.NewClient(t, core.Scheme, fake.RootSyncObjectV1Beta1(rootSyncName)),
					discoveryInterface: syncertest.NewDiscoveryClient(kinds.Namespace(), kinds.Role()),
					updater: updater{
						scope:     declared.RootReconciler,
						resources: &declared.Resources{},
					},
					mux: &sync.Mutex{},
				},
			}
			err := parseAndUpdate(context.Background(), parser, triggerReimport, &reconcilerState{})
			if err == nil {
				t.Errorf("parse() should return errors")
			}
		})
	}
}

func TestRoot_SourceReconcilerErrorsMetricValidation(t *testing.T) {
	testCases := []struct {
		name        string
		parseErrors []status.Error
		wantMetrics []*view.Row
	}{
		{
			name: "single reconciler error in source component",
			parseErrors: []status.Error{
				status.SourceError.Sprintf("source error").Build(),
			},
			wantMetrics: []*view.Row{
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
			},
		},
		{
			name: "multiple reconciler errors in source component",
			parseErrors: []status.Error{
				status.SourceError.Sprintf("source error").Build(),
				status.InternalError("internal error"),
			},
			wantMetrics: []*view.Row{
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := testmetrics.RegisterMetrics(metrics.ReconcilerErrorsView)

			parser := &root{
				sourceFormat: filesystem.SourceFormatUnstructured,
				opts: opts{
					parser:             &fakeParser{errors: tc.parseErrors},
					syncName:           rootSyncName,
					reconcilerName:     rootReconcilerName,
					client:             syncertest.NewClient(t, core.Scheme, fake.RootSyncObjectV1Beta1(rootSyncName)),
					discoveryInterface: syncertest.NewDiscoveryClient(kinds.Namespace(), kinds.Role()),
					updater: updater{
						scope:     declared.RootReconciler,
						resources: &declared.Resources{},
					},
					mux: &sync.Mutex{},
				},
			}
			err := parseAndUpdate(context.Background(), parser, triggerReimport, &reconcilerState{})
			if err == nil {
				t.Errorf("parse() should return errors")
			}
			if diff := m.ValidateMetrics(metrics.ReconcilerErrorsView, tc.wantMetrics); diff != "" {
				t.Errorf(diff)
			}
		})
	}
}

func TestRoot_SourceAndSyncReconcilerErrorsMetricValidation(t *testing.T) {
	testCases := []struct {
		name        string
		applyErrors []status.Error
		wantMetrics []*view.Row
	}{
		{
			name: "single reconciler error in sync component",
			applyErrors: []status.Error{
				applier.Error(errors.New("sync error")),
			},
			wantMetrics: []*view.Row{
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
			},
		},
		{
			name: "multiple reconciler errors in sync component",
			applyErrors: []status.Error{
				applier.Error(errors.New("sync error")),
				status.InternalError("internal error"),
			},
			wantMetrics: []*view.Row{
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "source"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
				{Data: &view.LastValueData{Value: 0}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "1xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "2xxx"}}},
				{Data: &view.LastValueData{Value: 1}, Tags: []tag.Tag{{Key: metrics.KeyComponent, Value: "sync"}, {Key: metrics.KeyErrorClass, Value: "9xxx"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := testmetrics.RegisterMetrics(metrics.ReconcilerErrorsView)

			parser := &root{
				sourceFormat: filesystem.SourceFormatUnstructured,
				opts: opts{
					parser: &fakeParser{},
					updater: updater{
						scope:      declared.RootReconciler,
						resources:  &declared.Resources{},
						remediator: &noOpRemediator{},
						applier:    &fakeApplier{errors: tc.applyErrors},
					},
					syncName:           rootSyncName,
					reconcilerName:     rootReconcilerName,
					client:             syncertest.NewClient(t, core.Scheme, fake.RootSyncObjectV1Beta1(rootSyncName)),
					discoveryInterface: syncertest.NewDiscoveryClient(kinds.Namespace(), kinds.Role()),
					mux:                &sync.Mutex{},
				},
			}
			err := parseAndUpdate(context.Background(), parser, triggerReimport, &reconcilerState{})
			if err == nil {
				t.Errorf("update() should return errors")
			}
			if diff := m.ValidateMetrics(metrics.ReconcilerErrorsView, tc.wantMetrics); diff != "" {
				t.Errorf(diff)
			}
		})
	}
}

func sortObjects(left, right client.Object) bool {
	leftID := core.IDOf(left)
	rightID := core.IDOf(right)
	return leftID.String() < rightID.String()
}

type fakeParser struct {
	parse  []ast.FileObject
	errors []status.Error
}

func (p *fakeParser) Parse(_ reader.FilePaths) ([]ast.FileObject, status.MultiError) {
	if p.errors == nil {
		return p.parse, nil
	}
	var errs status.MultiError
	for _, e := range p.errors {
		errs = status.Append(errs, e)
	}
	return nil, errs
}

func (p *fakeParser) ReadClusterRegistryResources(_ reader.FilePaths, _ filesystem.SourceFormat) ([]ast.FileObject, status.MultiError) {
	return nil, nil
}

func (p *fakeParser) ReadClusterNamesFromSelector(_ reader.FilePaths) ([]string, status.MultiError) {
	return nil, nil
}

type fakeApplier struct {
	got    []client.Object
	errors []status.Error
}

func (a *fakeApplier) Apply(_ context.Context, objs []client.Object) (map[schema.GroupVersionKind]struct{}, status.MultiError) {
	if a.errors == nil {
		a.got = objs
		gvks := make(map[schema.GroupVersionKind]struct{})
		for _, obj := range objs {
			gvks[obj.GetObjectKind().GroupVersionKind()] = struct{}{}
		}
		return gvks, nil
	}
	var errs status.MultiError
	for _, e := range a.errors {
		errs = status.Append(errs, e)
	}
	return nil, errs
}

func (a *fakeApplier) Errors() status.MultiError {
	var errs status.MultiError
	for _, e := range a.errors {
		errs = status.Append(errs, e)
	}
	return errs
}

func (a *fakeApplier) Syncing() bool {
	return false
}

func TestSummarizeErrors(t *testing.T) {
	testCases := []struct {
		name                 string
		sourceStatus         v1beta1.SourceStatus
		syncStatus           v1beta1.SyncStatus
		expectedErrorSources []v1beta1.ErrorSource
		expectedErrorSummary *v1beta1.ErrorSummary
	}{
		{
			name:                 "both sourceStatus and syncStatus are empty",
			sourceStatus:         v1beta1.SourceStatus{},
			syncStatus:           v1beta1.SyncStatus{},
			expectedErrorSources: nil,
			expectedErrorSummary: &v1beta1.ErrorSummary{},
		},
		{
			name: "sourceStatus is not empty (no trucation), syncStatus is empty",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus:           v1beta1.SyncStatus{},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                2,
				Truncated:                 false,
				ErrorCountAfterTruncation: 2,
			},
		},
		{
			name: "sourceStatus is not empty and trucates errors, syncStatus is empty",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus:           v1beta1.SyncStatus{},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                100,
				Truncated:                 true,
				ErrorCountAfterTruncation: 2,
			},
		},
		{
			name:         "sourceStatus is empty, syncStatus is not empty (no trucation)",
			sourceStatus: v1beta1.SourceStatus{},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                2,
				Truncated:                 false,
				ErrorCountAfterTruncation: 2,
			},
		},
		{
			name:         "sourceStatus is empty, syncStatus is not empty and trucates errors",
			sourceStatus: v1beta1.SourceStatus{},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                100,
				Truncated:                 true,
				ErrorCountAfterTruncation: 2,
			},
		},
		{
			name: "neither sourceStatus nor syncStatus is empty or trucates errors",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError, v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                4,
				Truncated:                 false,
				ErrorCountAfterTruncation: 4,
			},
		},
		{
			name: "neither sourceStatus nor syncStatus is empty, sourceStatus trucates errors",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError, v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                102,
				Truncated:                 true,
				ErrorCountAfterTruncation: 4,
			},
		},
		{
			name: "neither sourceStatus nor syncStatus is empty, syncStatus trucates errors",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                2,
					Truncated:                 false,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},

				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError, v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                102,
				Truncated:                 true,
				ErrorCountAfterTruncation: 4,
			},
		},
		{
			name: "neither sourceStatus nor syncStatus is empty, both trucates errors",
			sourceStatus: v1beta1.SourceStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "1021", ErrorMessage: "1021-error-message"},
					{Code: "1022", ErrorMessage: "1022-error-message"},
				},
				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			syncStatus: v1beta1.SyncStatus{
				Errors: []v1beta1.ConfigSyncError{
					{Code: "2009", ErrorMessage: "apiserver error"},
					{Code: "2009", ErrorMessage: "webhook error"},
				},

				ErrorSummary: &v1beta1.ErrorSummary{
					TotalCount:                100,
					Truncated:                 true,
					ErrorCountAfterTruncation: 2,
				},
			},
			expectedErrorSources: []v1beta1.ErrorSource{v1beta1.SourceError, v1beta1.SyncError},
			expectedErrorSummary: &v1beta1.ErrorSummary{
				TotalCount:                200,
				Truncated:                 true,
				ErrorCountAfterTruncation: 4,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotErrorSources, gotErrorSummary := summarizeErrors(tc.sourceStatus, tc.syncStatus)
			if diff := cmp.Diff(tc.expectedErrorSources, gotErrorSources); diff != "" {
				t.Errorf("summarizeErrors() got %v, expected %v", gotErrorSources, tc.expectedErrorSources)
			}
			if diff := cmp.Diff(tc.expectedErrorSummary, gotErrorSummary); diff != "" {
				t.Errorf("summarizeErrors() got %v, expected %v", gotErrorSummary, tc.expectedErrorSummary)
			}
		})
	}
}
