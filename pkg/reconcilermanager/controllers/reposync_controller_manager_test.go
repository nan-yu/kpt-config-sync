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

package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/watch"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/metadata"
	syncerFake "kpt.dev/configsync/pkg/syncer/syncertest/fake"
	watchutil "kpt.dev/configsync/pkg/util/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TestRepoSyncReconcilerDeploymentLifecycle validates that the
// RepoSyncReconciler works with the ControllerManager.
// - Create a ns-reconciler Deployment when a RepoSync is created
// - Delete the ns-reconciler Deployment when the RepoSync is deleted
func TestRepoSyncReconcilerDeploymentLifecycle(t *testing.T) {
	// Mock out parseDeployment for testing.
	parseDeployment = parsedDeployment

	t.Log("building RepoSyncReconciler")
	rs := repoSyncWithGit(reposyncNs, reposyncName, reposyncRef(gitRevision), reposyncBranch(branch), reposyncSecretType(configsync.AuthSSH), reposyncSecretRef(reposyncSSHKey))
	secretObj := secretObj(t, reposyncSSHKey, configsync.AuthSSH, v1beta1.GitSource, core.Namespace(rs.Namespace))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs := syncerFake.NewClientSet(t, core.Scheme)
	defer logObjectYAMLIfFailed(t, cs.Client, rs)

	errCh := startControllerManager(ctx, t, cs, setupNSReconciler(t, secretObj))

	// Wait for manager to exit before returning
	defer func() {
		cancel()
		t.Log("waiting for controller-manager to stop")
		for err := range errCh {
			require.NoError(t, err)
		}
	}()

	reconcilerKey := core.NsReconcilerObjectKey(rs.Namespace, rs.Name)

	t.Log("watching for reconciler deployment creation")
	watchCtx, watchCancel := context.WithTimeout(ctx, 10*time.Second)
	defer watchCancel()

	watcher, err := watchObjects(watchCtx, cs.Client, &appsv1.DeploymentList{})
	require.NoError(t, err)

	// Create RepoSync
	err = cs.Client.Create(ctx, rs)
	require.NoError(t, err)

	var reconcilerObj *appsv1.Deployment
	err = watchObjectUntil(ctx, cs.Client.Scheme(), watcher, reconcilerKey, func(event watch.Event) error {
		t.Logf("reconciler deployment %s", event.Type)
		if event.Type == watch.Added || event.Type == watch.Modified {
			reconcilerObj = event.Object.(*appsv1.Deployment)
			// success! deployment was applied.
			// Since there's no deployment controller,
			// don't wait for availability.
			return nil
		}
		// keep watching
		return errors.Errorf("reconciler deployment %s", event.Type)
	})
	require.NoError(t, err)
	if reconcilerObj == nil {
		t.Fatal("timed out waiting for reconciler deployment to be applied")
	}

	t.Log("verifying the reconciler-manager finalizer is present")
	rsKey := client.ObjectKeyFromObject(rs)
	rs = &v1beta1.RepoSync{}
	err = cs.Client.Get(ctx, rsKey, rs)
	require.NoError(t, err)
	require.True(t, controllerutil.ContainsFinalizer(rs, metadata.ReconcilerManagerFinalizer))

	t.Log("deleting sync object and watching for NotFound")
	err = watchutil.DeleteAndWait(ctx, cs.Client, rs, 10*time.Second)
	require.NoError(t, err)

	// All managed objects should have been deleted by the reconciler-manager finalizer.
	// Only the user Secret should remain.
	secretObj.SetUID("1")
	t.Log("verifying all managed objects were deleted")
	cs.Client.Check(t, secretObj)
}

// TestReconcileInvalidRepoSyncLifecycle validates that the RepoSyncReconciler
// handles the lifecycle of an invalid RepoSync object.
// - Surface an error for an invalid RepoSync object without generating any resources.
// - Delete the RepoSync object.
func TestReconcileInvalidRepoSyncLifecycle(t *testing.T) {
	// Mock out parseDeployment for testing.
	parseDeployment = parsedDeployment

	t.Log("building RepoSyncReconciler")
	// rs is an invalid RepoSync as its auth type is set to `token`, but the token key is not configured in the secret.
	rs := repoSyncWithGit(reposyncNs, reposyncName, reposyncRef(gitRevision), reposyncBranch(branch), reposyncSecretType(configsync.AuthToken), reposyncSecretRef(reposyncSSHKey))
	secretObj := secretObj(t, reposyncSSHKey, configsync.AuthSSH, v1beta1.GitSource, core.Namespace(rs.Namespace))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs := syncerFake.NewClientSet(t, core.Scheme)
	defer logObjectYAMLIfFailed(t, cs.Client, rs)

	errCh := startControllerManager(ctx, t, cs, setupNSReconciler(t, secretObj))

	// Wait for manager to exit before returning
	defer func() {
		cancel()
		t.Log("waiting for controller-manager to stop")
		for err := range errCh {
			require.NoError(t, err)
		}
	}()

	t.Log("watching for RepoSync status update")
	watchCtx, watchCancel := context.WithTimeout(ctx, 10*time.Second)
	defer watchCancel()

	watcher, err := watchObjects(watchCtx, cs.Client, &v1beta1.RepoSyncList{})
	require.NoError(t, err)

	t.Log("creating RepoSync")
	err = cs.Client.Create(ctx, rs)
	require.NoError(t, err)

	var rsObj *v1beta1.RepoSync
	err = watchObjectUntil(ctx, cs.Client.Scheme(), watcher, core.ObjectNamespacedName(rs), func(event watch.Event) error {
		t.Logf("RepoSync %s", event.Type)
		if event.Type == watch.Modified {
			rsObj = event.Object.(*v1beta1.RepoSync)
			for _, cond := range rsObj.Status.Conditions {
				if cond.Reason == "Validation" && cond.Message == `git secretType was set as "token" but token key is not present in ssh-key secret` {
					return nil
				}
			}
			return fmt.Errorf("RepoSync status not updated yet")
		}
		// keep watching
		return fmt.Errorf("RepoSync object %s", event.Type)
	})
	require.NoError(t, err)
	if rsObj == nil {
		t.Fatal("timed out waiting for RepoSync to become stalled")
	}

	t.Log("only the stalled RepoSync and user Secret should be present, no other generated resources")
	secretObj.SetUID("1")
	cs.Client.Check(t, secretObj, rsObj)

	t.Log("deleting sync object and watching for NotFound")
	err = watchutil.DeleteAndWait(ctx, cs.Client, rs, 10*time.Second)
	require.NoError(t, err)
	t.Log("only the user Secret should be present")
	cs.Client.Check(t, secretObj)
}

// TestReconcileRepoSyncLifecycleValidToInvalid validates that the RepoSyncReconciler
// handles the lifecycle of an RepoSync object changing from valid to invalid state.
// - Create a ns-reconciler Deployment when a valid RepoSync is created
// - Surface an error when the RepoSync object becomes invalid without deleting the generated resources
// - Delete the RepoSync object and its generated dependencies.
func TestReconcileRepoSyncLifecycleValidToInvalid(t *testing.T) {
	// Mock out parseDeployment for testing.
	parseDeployment = parsedDeployment

	t.Log("building RepoSyncReconciler")
	rs := repoSyncWithGit(reposyncNs, reposyncName, reposyncRef(gitRevision), reposyncBranch(branch), reposyncSecretType(configsync.AuthSSH), reposyncSecretRef(reposyncSSHKey))
	secretObj := secretObj(t, reposyncSSHKey, configsync.AuthSSH, v1beta1.GitSource, core.Namespace(rs.Namespace))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs := syncerFake.NewClientSet(t, core.Scheme)
	defer logObjectYAMLIfFailed(t, cs.Client, rs)

	errCh := startControllerManager(ctx, t, cs, setupNSReconciler(t, secretObj))

	// Wait for manager to exit before returning
	defer func() {
		cancel()
		t.Log("waiting for controller-manager to stop")
		for err := range errCh {
			require.NoError(t, err)
		}
	}()

	reconcilerKey := core.NsReconcilerObjectKey(rs.Namespace, rs.Name)

	t.Log("watching for reconciler deployment creation")
	watchCtx, watchCancel := context.WithTimeout(ctx, 10*time.Second)
	defer watchCancel()

	watcher, err := watchObjects(watchCtx, cs.Client, &appsv1.DeploymentList{})
	require.NoError(t, err)

	// Create RepoSync
	err = cs.Client.Create(ctx, rs)
	require.NoError(t, err)

	var reconcilerObj *appsv1.Deployment
	err = watchObjectUntil(ctx, cs.Client.Scheme(), watcher, reconcilerKey, func(event watch.Event) error {
		t.Logf("reconciler deployment %s", event.Type)
		if event.Type == watch.Added || event.Type == watch.Modified {
			reconcilerObj = event.Object.(*appsv1.Deployment)
			// success! deployment was applied.
			// Since there's no deployment controller,
			// don't wait for availability.
			return nil
		}
		// keep watching
		return errors.Errorf("reconciler deployment %s", event.Type)
	})
	require.NoError(t, err)
	if reconcilerObj == nil {
		t.Fatal("timed out waiting for reconciler deployment to be applied")
	}

	t.Log("verifying the reconciler-manager finalizer is present")
	rsKey := client.ObjectKeyFromObject(rs)
	rs = &v1beta1.RepoSync{}
	err = cs.Client.Get(ctx, rsKey, rs)
	require.NoError(t, err)
	require.True(t, controllerutil.ContainsFinalizer(rs, metadata.ReconcilerManagerFinalizer))

	t.Log("watching for RepoSync status update")
	watcher, err = watchObjects(watchCtx, cs.Client, &v1beta1.RepoSyncList{})
	require.NoError(t, err)

	t.Log("updating RepoSync to make it invalid")
	rs.Spec.Auth = configsync.AuthToken
	err = cs.Client.Update(ctx, rs)
	require.NoError(t, err)

	var rsObj *v1beta1.RepoSync
	err = watchObjectUntil(ctx, cs.Client.Scheme(), watcher, core.ObjectNamespacedName(rs), func(event watch.Event) error {
		t.Logf("RepoSync %s", event.Type)
		if event.Type == watch.Modified {
			rsObj = event.Object.(*v1beta1.RepoSync)
			for _, cond := range rsObj.Status.Conditions {
				if cond.Reason == "Validation" && cond.Message == `git secretType was set as "token" but token key is not present in ssh-key secret` {
					return nil
				}
			}
			return fmt.Errorf("RepoSync status not updated yet")
		}
		// keep watching
		return fmt.Errorf("RepoSync object %s", event.Type)
	})
	require.NoError(t, err)
	if rsObj == nil {
		t.Fatal("timed out waiting for RepoSync to become stalled")
	}

	t.Log("verifying the reconciler deployment object still exists")
	err = cs.Client.Get(ctx, reconcilerKey, &appsv1.Deployment{})
	require.NoError(t, err)

	t.Log("deleting sync object and watching for NotFound")
	err = watchutil.DeleteAndWait(ctx, cs.Client, rs, 10*time.Second)
	require.NoError(t, err)

	// All managed objects should have been deleted by the reconciler-manager finalizer.
	// Only the user Secret should remain.
	t.Log("verifying all managed objects were deleted")
	secretObj.SetUID("1")
	cs.Client.Check(t, secretObj)
}

// TestRepoSyncReconcilerDeploymentDriftProtection validates that changes to
// specific managed fields of the reconciler deployment are reverted if changed
// by another client.
func TestRepoSyncReconcilerDeploymentDriftProtection(t *testing.T) {
	exampleObj := &appsv1.Deployment{}
	objKeyFunc := func(rs client.ObjectKey) client.ObjectKey {
		// reconciler-manager managed reconciler deployment
		return core.NsReconcilerObjectKey(rs.Namespace, rs.Name)
	}
	var oldObj *appsv1.Deployment
	var oldValue string
	modify := func(obj client.Object) error {
		oldObj = obj.(*appsv1.Deployment)
		oldValue = oldObj.Spec.Template.Spec.ServiceAccountName
		oldObj.Spec.Template.Spec.ServiceAccountName = "seanboswell"
		return nil
	}
	validate := func(obj client.Object) error {
		newObj := obj.(*appsv1.Deployment)
		newValue := newObj.Spec.Template.Spec.ServiceAccountName
		if newValue != oldValue {
			// keep watching
			return errors.Errorf("spec.template.spec.serviceAccountName expected to be %q, but found %q",
				oldValue, newValue)
		}
		newRV, err := parseResourceVersion(newObj)
		if err != nil {
			return err
		}
		// ResourceVersion should be updated on the oldObj by the client.Update AFTER the modify func was called.
		oldRV, err := parseResourceVersion(oldObj)
		if err != nil {
			return err
		}
		if newRV <= oldRV {
			return errors.Errorf("watch event with resourceVersion %d predates expected update with resourceVersion %d",
				newRV, oldRV)
		}
		// success - change reverted
		return nil
	}
	testRepoSyncDriftProtection(t, exampleObj, objKeyFunc, modify, validate)
}

// TestRepoSyncReconcilerServiceAccountDriftProtection validates that changes to
// specific managed fields of the reconciler service account are reverted if
// changed by another client.
func TestRepoSyncReconcilerServiceAccountDriftProtection(t *testing.T) {
	exampleObj := &corev1.ServiceAccount{}
	objKeyFunc := func(rs client.ObjectKey) client.ObjectKey {
		// reconciler-manager managed service account
		return core.NsReconcilerObjectKey(rs.Namespace, rs.Name)
	}
	var oldObj *corev1.ServiceAccount
	var oldValue string
	modify := func(obj client.Object) error {
		oldObj = obj.(*corev1.ServiceAccount)
		oldValue = oldObj.Labels[metadata.SyncKindLabel]
		oldObj.Labels[metadata.SyncKindLabel] = "seanboswell"
		return nil
	}
	validate := func(obj client.Object) error {
		newObj := obj.(*corev1.ServiceAccount)
		newValue := newObj.Labels[metadata.SyncKindLabel]
		if newValue != oldValue {
			// keep watching
			return errors.Errorf("spec.metadata.labels[%q] expected to be %q, but found %q",
				metadata.SyncKindLabel, oldValue, newValue)
		}
		newRV, err := parseResourceVersion(newObj)
		if err != nil {
			return err
		}
		// ResourceVersion should be updated on the oldObj by the client.Update AFTER the modify func was called.
		oldRV, err := parseResourceVersion(oldObj)
		if err != nil {
			return err
		}
		if newRV <= oldRV {
			return errors.Errorf("watch event with resourceVersion %d predates expected update with resourceVersion %d",
				newRV, oldRV)
		}
		// success - change reverted
		return nil
	}
	testRepoSyncDriftProtection(t, exampleObj, objKeyFunc, modify, validate)
}

// TestRepoSyncReconcilerRoleBindingDriftProtection validates that changes to
// specific managed fields of the reconciler role binding are reverted if
// changed by another client.
func TestRepoSyncReconcilerRoleBindingDriftProtection(t *testing.T) {
	exampleObj := &rbacv1.RoleBinding{}
	objKeyFunc := func(syncRef client.ObjectKey) client.ObjectKey {
		// reconciler-manager managed robe binding
		return client.ObjectKey{
			Namespace: syncRef.Namespace,
			Name:      RepoSyncPermissionsName(),
		}
	}
	var oldObj *rbacv1.RoleBinding
	var oldValue string
	modify := func(obj client.Object) error {
		oldObj = obj.(*rbacv1.RoleBinding)
		oldValue = oldObj.RoleRef.Name
		oldObj.RoleRef.Name = "seanboswell"
		return nil
	}
	validate := func(obj client.Object) error {
		newObj := obj.(*rbacv1.RoleBinding)
		newValue := newObj.RoleRef.Name
		if newValue != oldValue {
			// keep watching
			return errors.Errorf("roleRef.name expected to be %q, but found %q",
				oldValue, newValue)
		}
		newRV, err := parseResourceVersion(newObj)
		if err != nil {
			return err
		}
		// ResourceVersion should be updated on the oldObj by the client.Update AFTER the modify func was called.
		oldRV, err := parseResourceVersion(oldObj)
		if err != nil {
			return err
		}
		if newRV <= oldRV {
			return errors.Errorf("watch event with resourceVersion %d predates expected update with resourceVersion %d",
				newRV, oldRV)
		}
		// success - change reverted
		return nil
	}
	testRepoSyncDriftProtection(t, exampleObj, objKeyFunc, modify, validate)
}

// TestRepoSyncReconcilerAuthSecretDriftProtection validates that changes to
// specific managed fields of the reconciler auth secret are reverted if changed
// by another client.
func TestRepoSyncReconcilerAuthSecretDriftProtection(t *testing.T) {
	exampleObj := &corev1.Secret{}
	objKeyFunc := func(syncRef client.ObjectKey) client.ObjectKey {
		reconcilerRef := core.NsReconcilerObjectKey(syncRef.Namespace, syncRef.Name)
		// reconciler-manager managed auth secret
		return client.ObjectKey{
			Namespace: reconcilerRef.Namespace,
			Name:      ReconcilerResourceName(reconcilerRef.Name, reposyncSSHKey),
		}
	}
	var oldObj *corev1.Secret
	var oldValue string
	modify := func(obj client.Object) error {
		oldObj = obj.(*corev1.Secret)
		oldValue = string(oldObj.Data[string(configsync.AuthSSH)])
		oldObj.Data[string(configsync.AuthSSH)] = []byte("seanboswell")
		return nil
	}
	validate := func(obj client.Object) error {
		newObj := obj.(*corev1.Secret)
		newValue := string(newObj.Data[string(configsync.AuthSSH)])
		if newValue != oldValue {
			// keep watching
			return errors.Errorf("data[%q] expected to be %q, but found %q",
				configsync.AuthSSH, oldValue, newValue)
		}
		newRV, err := parseResourceVersion(newObj)
		if err != nil {
			return err
		}
		// ResourceVersion should be updated on the oldObj by the client.Update AFTER the modify func was called.
		oldRV, err := parseResourceVersion(oldObj)
		if err != nil {
			return err
		}
		if newRV <= oldRV {
			return errors.Errorf("watch event with resourceVersion %d predates expected update with resourceVersion %d",
				newRV, oldRV)
		}
		// success - change reverted
		return nil
	}
	testRepoSyncDriftProtection(t, exampleObj, objKeyFunc, modify, validate)
}

func testRepoSyncDriftProtection(t *testing.T, exampleObj client.Object, objKeyFunc func(client.ObjectKey) client.ObjectKey, modify, validate func(client.Object) error) {
	t.Log("building RepoSyncReconciler")
	syncObj := repoSyncWithGit(reposyncNs, reposyncName, reposyncRef(gitRevision), reposyncBranch(branch), reposyncSecretType(configsync.AuthSSH), reposyncSecretRef(reposyncSSHKey))
	secretObj := secretObj(t, reposyncSSHKey, configsync.AuthSSH, v1beta1.GitSource, core.Namespace(syncObj.Namespace))
	testDriftProtection(t, setupNSReconciler(t, secretObj), syncObj, exampleObj, objKeyFunc, modify, validate)
}
