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

package nomostest

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	testmetrics "kpt.dev/configsync/e2e/nomostest/metrics"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	"kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/pkg/api/configmanagement"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1alpha1"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/importer/filesystem"
	"kpt.dev/configsync/pkg/importer/filesystem/cmpath"
	"kpt.dev/configsync/pkg/importer/reader"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/metadata"
	"kpt.dev/configsync/pkg/metrics"
	"kpt.dev/configsync/pkg/reconcilermanager"
	"kpt.dev/configsync/pkg/reconcilermanager/controllers"
	"kpt.dev/configsync/pkg/status"
	"kpt.dev/configsync/pkg/testing/fake"
	webhookconfig "kpt.dev/configsync/pkg/webhook/configuration"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AcmeDir is the sync directory of the test source repository.
	AcmeDir = "acme"
	// Manifests is the folder of the test manifests
	Manifests = "manifests"

	// e2e/raw-nomos/manifests/multi-repo-configmaps.yaml
	multiConfigMapsName = "multi-repo-configmaps.yaml"

	// clusterRoleName imitates a user-created (Cluster)Role for NS reconcilers.
	clusterRoleName = "c2-e2e"
)

var (
	// baseDir is the path to the Nomos repository root from test case files.
	//
	// All paths must be relative to the test file that is running. There is probably
	// a more elegant way to do this.
	baseDir            = filepath.FromSlash("../..")
	outputManifestsDir = filepath.Join(baseDir, ".output", "staging", "oss")
	configSyncManifest = filepath.Join(outputManifestsDir, "config-sync-manifest.yaml")

	multiConfigMaps = filepath.Join(baseDir, "e2e", "raw-nomos", Manifests, multiConfigMapsName)
)

var (
	// reconcilerPollingPeriod specifies reconciler polling period as time.Duration
	reconcilerPollingPeriod time.Duration
	// hydrationPollingPeriod specifies hydration-controller polling period as time.Duration
	hydrationPollingPeriod time.Duration
)

// IsReconcilerManagerConfigMap returns true if passed obj is the
// reconciler-manager ConfigMap reconciler-manager-cm in config-management namespace.
func IsReconcilerManagerConfigMap(obj client.Object) bool {
	return obj.GetName() == "reconciler-manager-cm" &&
		obj.GetNamespace() == "config-management-system" &&
		obj.GetObjectKind().GroupVersionKind() == kinds.ConfigMap()
}

// ResetReconcilerManagerConfigMap resets the reconciler manager config map
// to what is defined in the manifest
func ResetReconcilerManagerConfigMap(nt *NT) error {
	nt.T.Helper()
	objs := parseManifests(nt)
	for _, obj := range objs {
		if !IsReconcilerManagerConfigMap(obj) {
			continue
		}
		nt.T.Logf("ResetReconcilerManagerConfigMap obj: %v", core.GKNN(obj))
		err := nt.Update(obj)
		if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("failed to reset reconciler manager ConfigMap")
}

func parseManifests(nt *NT) []client.Object {
	nt.T.Helper()
	tmpManifestsDir := filepath.Join(nt.TmpDir, Manifests)

	objs := installationManifests(nt, tmpManifestsDir)
	objs = convertObjects(nt, objs)
	reconcilerPollingPeriod = nt.ReconcilerPollingPeriod
	hydrationPollingPeriod = nt.HydrationPollingPeriod
	objs = multiRepoObjects(nt.T, objs, setReconcilerDebugMode, setPollingPeriods)

	return objs
}

// installConfigSync installs ConfigSync on the test cluster, and returns a
// callback for checking that the installation succeeded.
func installConfigSync(nt *NT, nomos ntopts.Nomos) {
	nt.T.Helper()
	objs := parseManifests(nt)
	for _, o := range objs {
		nt.T.Logf("installConfigSync obj: %v", core.GKNN(o))
		if o.GetObjectKind().GroupVersionKind().GroupKind() == kinds.ConfigMap().GroupKind() && o.GetName() == reconcilermanager.SourceFormat {
			cm := o.(*corev1.ConfigMap)
			cm.Data[filesystem.SourceFormatKey] = string(nomos.SourceFormat)
		}

		err := nt.Create(o)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				continue
			}
			nt.T.Fatal(err)
		}
	}
}

func isPSPCluster() bool {
	return strings.Contains(testing.GCPClusterFromEnv, "psp")
}

// convertObjects converts objects to their literal types. We can do this as
// we should have all required types in the scheme anyway. This keeps us from
// having to do ugly Unstructured operations.
func convertObjects(nt *NT, objs []client.Object) []client.Object {
	result := make([]client.Object, len(objs))
	for i, obj := range objs {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			// Already converted when read from the disk, or added manually.
			// We don't need to convert, so insert and go to the next element.
			result[i] = obj
			continue
		}

		o, err := nt.scheme.New(u.GroupVersionKind())
		if err != nil {
			nt.T.Fatalf("installed type %v not in Scheme: %v", u.GroupVersionKind(), err)
		}

		jsn, err := u.MarshalJSON()
		if err != nil {
			nt.T.Fatalf("marshalling object into JSON: %v", err)
		}

		err = json.Unmarshal(jsn, o)
		if err != nil {
			nt.T.Fatalf("unmarshalling JSON into object: %v", err)
		}
		newObj, ok := o.(client.Object)
		if !ok {
			nt.T.Fatalf("trying to install non-object type %v", u.GroupVersionKind())
		}
		result[i] = newObj
	}
	return result
}

func copyFile(src, dst string) error {
	bytes, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dst, bytes, fileMode)
}

func copyDirContents(src, dest string) error {
	files, err := ioutil.ReadDir(src)
	if err != nil {
		return err
	}

	for _, f := range files {
		// Explicitly not recursive
		if f.IsDir() {
			continue
		}

		from := filepath.Join(src, f.Name())
		to := filepath.Join(dest, f.Name())
		err := copyFile(from, to)
		if err != nil {
			return err
		}
	}
	return nil
}

// installationManifests generates the ConfigSync installation YAML and copies
// it to the test's temporary directory.
func installationManifests(nt *NT, tmpManifestsDir string) []client.Object {
	nt.T.Helper()
	err := os.MkdirAll(tmpManifestsDir, fileMode)
	if err != nil {
		nt.T.Fatal(err)
	}

	// Copy all manifests to temporary dir
	if err := copyDirContents(outputManifestsDir, tmpManifestsDir); err != nil {
		nt.T.Fatal(err)
	}
	if err := copyFile(multiConfigMaps, filepath.Join(tmpManifestsDir, multiConfigMapsName)); err != nil {
		nt.T.Fatal(err)
	}

	// Create the list of paths for the File to read.
	readPath, err := cmpath.AbsoluteOS(tmpManifestsDir)
	if err != nil {
		nt.T.Fatal(err)
	}
	files, err := ioutil.ReadDir(tmpManifestsDir)
	if err != nil {
		nt.T.Fatal(err)
	}
	paths := make([]cmpath.Absolute, len(files))
	for i, f := range files {
		paths[i] = readPath.Join(cmpath.RelativeSlash(f.Name()))
	}
	// Read the manifests cached in the tmpdir.
	r := reader.File{}
	filePaths := reader.FilePaths{
		RootDir: readPath,
		Files:   paths,
	}
	fos, err := r.Read(filePaths)
	if err != nil {
		nt.T.Fatal(err)
	}

	var objs []client.Object
	for _, o := range fos {
		objs = append(objs, o.Unstructured)
	}
	return objs
}

func multiRepoObjects(t testing.NTB, objects []client.Object, opts ...func(t testing.NTB, obj client.Object)) []client.Object {
	var filtered []client.Object
	found := false
	for _, obj := range objects {
		if !isPSPCluster() && obj.GetName() == "acm-psp" {
			continue
		}
		if IsReconcilerManagerConfigMap(obj) {
			// Mark that we've found the ReconcilerManager ConfigMap.
			// This way we know we've enabled debug mode.
			found = true
		}
		for _, opt := range opts {
			opt(t, obj)
		}
		filtered = append(filtered, obj)
	}
	if !found {
		t.Fatal("Did not find Reconciler Manager ConfigMap")
	}
	return filtered
}

// ValidateMultiRepoDeployments validates if all Config Sync Components are available.
func ValidateMultiRepoDeployments(nt *NT) error {
	rs := RootSyncObjectV1Beta1FromRootRepo(nt, configsync.RootSyncName)
	if err := nt.Create(rs); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			nt.T.Fatal(err)
		}
	}

	deployments := map[client.ObjectKey]time.Duration{
		{Name: reconcilermanager.ManagerName, Namespace: configmanagement.ControllerNamespace}:       nt.DefaultWaitTimeout,
		{Name: DefaultRootReconcilerName, Namespace: configmanagement.ControllerNamespace}:           nt.DefaultWaitTimeout,
		{Name: webhookconfig.ShortName, Namespace: configmanagement.ControllerNamespace}:             nt.DefaultWaitTimeout * 2,
		{Name: metrics.OtelCollectorName, Namespace: metrics.MonitoringNamespace}:                    nt.DefaultWaitTimeout,
		{Name: configmanagement.RGControllerName, Namespace: configmanagement.RGControllerNamespace}: nt.DefaultWaitTimeout,
	}

	var wg sync.WaitGroup
	errCh := make(chan error)

	// Wait for deployments asynchronously, for more accurate time reporting.
	for deployment, timeout := range deployments {
		wg.Add(1)
		timeoutCopy := timeout // copy loop var for use in goroutine
		go func(key client.ObjectKey) {
			defer wg.Done()
			took, err := Retry(timeoutCopy, func() error {
				return nt.Validate(key.Name, key.Namespace,
					&appsv1.Deployment{}, isAvailableDeployment)
			})
			if err != nil {
				errCh <- err
				return
			}
			nt.T.Logf("took %v to wait for Deployment %s", took, key)
		}(deployment)
	}

	// Close error channel when all retry loops are done.
	go func() {
		wg.Wait()
		close(errCh)
	}()

	// Collect errors until error channel is closed.
	var errs status.MultiError
	for err := range errCh {
		errs = status.Append(errs, err)
	}
	if errs != nil {
		return errs
	}

	// Validate the webhook config separately, since it's not a Deployment.
	if !*nt.WebhookDisabled {
		took, err := Retry(nt.DefaultWaitTimeout, func() error {
			return nt.Validate("admission-webhook.configsync.gke.io", "", &admissionv1.ValidatingWebhookConfiguration{})
		})
		if err != nil {
			return err
		}
		nt.T.Logf("took %v to wait for %s %s", took, "ValidatingWebhookConfiguration", "admission-webhook.configsync.gke.io")
	}

	return validateMultiRepoPods(nt)
}

// validateMultiRepoPods validates if all Config Sync Pods are healthy. It doesn't retry
// because it is supposed to be invoked after validateMultiRepoDeployments succeeds.
// It only checks the central root reconciler Pod.
func validateMultiRepoPods(nt *NT) error {
	for _, ns := range CSNamespaces {
		pods := corev1.PodList{}
		if err := nt.List(&pods, client.InNamespace(ns)); err != nil {
			return err
		}
		for _, pod := range pods.Items {
			if pod.Labels["app"] == "reconciler" {
				if pod.Labels[metadata.SyncKindLabel] != configsync.RootSyncKind || pod.Labels[metadata.SyncNameLabel] != configsync.RootSyncName {
					continue // Only validate the central root reconciler Pod but skip other reconcilers because other RSync objects might be managed by the central root reconciler and may not be reconciled yet.
				}
			}
			// Check if the Pod (running Pod only) has any former abnormally terminated container.
			// If the Pod is already in a terminating state (with a deletion timestamp) due to clean up, ignore the check.
			// It uses pod.DeletionTimestamp instead of pod.Status.Phase because the Pod may not be scheduled to evict yet.
			if pod.DeletionTimestamp == nil {
				if err := nt.Validate(pod.Name, pod.Namespace, &corev1.Pod{}, noOOMKilledContainer(nt)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// noOOMKilledContainer is a predicate to validate if the Pod has a container that was terminated due to OOMKilled
// It doesn't check other non-zero exit code because the admission-webhook sometimes exits with 1 and then recovers after restart.
// Example log:
//
//	kubectl logs admission-webhook-5c79b59f86-fzqgl -n config-management-system -p
//	I0826 07:54:47.435824       1 setup.go:31] Build Version: v1.13.0-rc.5-2-gef2d1ac-dirty
//	I0826 07:54:47.435992       1 logr.go:249] setup "msg"="starting manager"
//	E0826 07:55:17.436921       1 logr.go:265]  "msg"="Failed to get API Group-Resources" "error"="Get \"https://10.96.0.1:443/api?timeout=32s\": dial tcp 10.96.0.1:443: i/o timeout"
//	E0826 07:55:17.436945       1 logr.go:265] setup "msg"="starting manager" "error"="Get \"https://10.96.0.1:443/api?timeout=32s\": dial tcp 10.96.0.1:443: i/o timeout"
func noOOMKilledContainer(nt *NT) Predicate {
	return func(o client.Object) error {
		pod, ok := o.(*corev1.Pod)
		if !ok {
			return WrongTypeErr(o, pod)
		}

		for _, cs := range pod.Status.ContainerStatuses {
			terminated := cs.LastTerminationState.Terminated
			if terminated != nil && terminated.ExitCode != 0 && terminated.Reason == "OOMKilled" {
				// Display the full state of the malfunctioning Pod to aid in debugging.
				jsn, err := json.MarshalIndent(pod, "", "  ")
				if err != nil {
					return err
				}
				// Display the logs for the previous instance of the container.
				args := []string{"logs", pod.Name, "-n", pod.Namespace, "-p"}
				out, err := nt.Kubectl(args...)
				// Print a standardized header before each printed log to make ctrl+F-ing the
				// log you want easier.
				cmd := fmt.Sprintf("kubectl %s", strings.Join(args, " "))
				if err != nil {
					nt.T.Logf("failed to run %q: %v\n%s", cmd, err, out)
				}
				return fmt.Errorf("%w for pod/%s in namespace %q, container %q terminated with exit code %d and reason %q\n\n%s\n\n%s",
					ErrFailedPredicate, pod.Name, pod.Namespace, cs.Name, terminated.ExitCode, terminated.Reason, string(jsn), fmt.Sprintf("%s\n%s", cmd, out))
			}
		}
		return nil
	}
}

func setupRootSync(nt *NT, rsName string) {
	// create RootSync to initialize the root reconciler.
	rs := RootSyncObjectV1Beta1FromRootRepo(nt, rsName)
	if err := nt.Create(rs); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			nt.T.Fatal(err)
		}
	}
}

func setupRepoSync(nt *NT, nn types.NamespacedName) {
	// create RepoSync to initialize the Namespace reconciler.
	rs := RepoSyncObjectV1Beta1FromNonRootRepo(nt, nn)
	if err := nt.Create(rs); err != nil {
		nt.T.Fatal(err)
	}
}

func waitForReconciler(nt *NT, name string) error {
	took, err := Retry(60*time.Second, func() error {
		return nt.Validate(name, configmanagement.ControllerNamespace,
			&appsv1.Deployment{}, isAvailableDeployment)
	})
	if err != nil {
		return err
	}
	nt.T.Logf("took %v to wait for %s", took, name)

	return nil
}

// RepoSyncClusterRole returns clusterrole with permissions to manage resources in the cluster.
func RepoSyncClusterRole() *rbacv1.ClusterRole {
	cr := fake.ClusterRoleObject(core.Name(clusterRoleName))
	// TODO: make this more granular by test options
	cr.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{rbacv1.APIGroupAll},
			Resources: []string{rbacv1.ResourceAll},
			Verbs:     []string{rbacv1.VerbAll},
		},
	}
	if isPSPCluster() {
		cr.Rules = append(cr.Rules, rbacv1.PolicyRule{
			APIGroups:     []string{"policy"},
			Resources:     []string{"podsecuritypolicies"},
			ResourceNames: []string{"acm-psp"},
			Verbs:         []string{"use"},
		})
	}
	return cr
}

// RepoSyncRoleBinding returns rolebinding that grants service account
// permission to manage resources in the namespace.
func RepoSyncRoleBinding(nn types.NamespacedName) *rbacv1.RoleBinding {
	rb := fake.RoleBindingObject(core.Name(nn.Name), core.Namespace(nn.Namespace))
	sb := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      core.NsReconcilerName(nn.Namespace, nn.Name),
			Namespace: configmanagement.ControllerNamespace,
		},
	}
	rf := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     clusterRoleName,
	}
	rb.Subjects = sb
	rb.RoleRef = rf
	return rb
}

// repoSyncClusterRoleBinding returns clusterrolebinding that grants service account
// permission to manage resources in the namespace.
func repoSyncClusterRoleBinding(nn types.NamespacedName) *rbacv1.ClusterRoleBinding {
	rb := fake.ClusterRoleBindingObject(core.Name(nn.Name + "-" + nn.Namespace))
	sb := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      core.NsReconcilerName(nn.Namespace, nn.Name),
			Namespace: configmanagement.ControllerNamespace,
		},
	}
	rf := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     clusterRoleName,
	}
	rb.Subjects = sb
	rb.RoleRef = rf
	return rb
}

func setupRepoSyncRoleBinding(nt *NT, nn types.NamespacedName) error {
	if err := nt.Create(RepoSyncRoleBinding(nn)); err != nil {
		nt.T.Fatal(err)
	}

	// Validate rolebinding is present.
	return nt.Validate(nn.Name, nn.Namespace, &rbacv1.RoleBinding{})
}

func revokeRepoSyncClusterRoleBinding(nt *NT, nn types.NamespacedName) {
	if err := nt.Delete(repoSyncClusterRoleBinding(nn)); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		nt.T.Fatal(err)
	}
	WaitForNotFound(nt, kinds.ClusterRoleBinding(), nn.Name+"-"+nn.Namespace, "")
}

func revokeRepoSyncNamespace(nt *NT, ns string) {
	// TODO: Ideally we can delete the namespace directly and check if it is terminated.
	// Due to b/184680603, we have to check if the namespace is in a terminating state to avoid the error:
	//   Operation cannot be fulfilled on namespaces "bookstore": The system is ensuring all content is removed from this namespace.
	//   Upon completion, this namespace will automatically be purged by the system.
	namespace := &corev1.Namespace{}
	if err := nt.Get(ns, "", namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		nt.T.Fatal(err)
	}
	if namespace.Status.Phase != corev1.NamespaceTerminating {
		if err := nt.Delete(fake.NamespaceObject(ns)); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			nt.T.Fatal(err)
		}
	}
	WaitForNotFound(nt, kinds.Namespace(), ns, "")
}

// setReconcilerDebugMode ensures the Reconciler deployments are run in debug mode.
func setReconcilerDebugMode(t testing.NTB, obj client.Object) {
	if !IsReconcilerManagerConfigMap(obj) {
		return
	}

	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("parsed Reconciler Manager ConfigMap was %T %v", obj, obj)
	}

	key := "deployment.yaml"
	deploymentYAML, found := cm.Data[key]
	if !found {
		t.Fatal("Reconciler Manager ConfigMap has no deployment.yaml entry")
	}

	// The Deployment YAML for Reconciler deployments is a raw YAML string embedded
	// in the ConfigMap. Unmarshalling/marshalling is likely to lead to errors, so
	// this modifies the YAML string directly by finding the line we want to insert
	// the debug flag after, and then inserting the line we want to add.
	lines := strings.Split(deploymentYAML, "\n")
	found = false
	for i, line := range lines {
		// We want to set the debug flag immediately after setting the source-dir flag.
		if strings.Contains(line, "- \"--source-dir=/repo/source/rev\"") {
			// Standard Go "insert into slice" idiom.
			lines = append(lines, "")
			copy(lines[i+2:], lines[i+1:])
			// Prefix of 8 spaces as the run arguments are indented 8 spaces relative
			// to the embedded YAML string. The embedded YAML is indented 3 spaces,
			// so this is equivalent to indenting 11 spaces in the original file:
			// manifests/templates/reconciler-manager-configmap.yaml.
			lines[i+1] = "        - \"--debug\""
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Unable to set debug mode for reconciler")
	}

	cm.Data[key] = strings.Join(lines, "\n")
	t.Log("Set deployment.yaml")
}

// setPollingPeriods update Reconciler Manager configmap
// reconciler-manager-cm with reconciler and hydration-controller polling
// periods to override the default.
func setPollingPeriods(t testing.NTB, obj client.Object) {
	if !IsReconcilerManagerConfigMap(obj) {
		return
	}

	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("parsed Reconciler Manager ConfigMap was not ConfigMap %T %v", obj, obj)
	}

	cm.Data[reconcilermanager.ReconcilerPollingPeriod] = reconcilerPollingPeriod.String()
	cm.Data[reconcilermanager.HydrationPollingPeriod] = hydrationPollingPeriod.String()
	t.Log("Set filesystem polling period")
}

func setupDelegatedControl(nt *NT, opts *ntopts.New) {
	// Just create one RepoSync ClusterRole, even if there are no Namespace repos.
	if err := nt.Create(RepoSyncClusterRole()); err != nil {
		nt.T.Fatal(err)
	}

	for rsName := range opts.RootRepos {
		// The default RootSync is created manually, no need to set up again.
		if rsName == configsync.RootSyncName {
			continue
		}
		setupRootSync(nt, rsName)
	}

	for nn := range opts.NamespaceRepos {
		// Add a ClusterRoleBinding so that the pods can be created
		// when the cluster has PodSecurityPolicy enabled.
		// Background: If a RoleBinding (not a ClusterRoleBinding) is used,
		// it will only grant usage for pods being run in the same namespace as the binding.
		// TODO: Remove the psp related change when Kubernetes 1.25 is
		// available on GKE.
		if isPSPCluster() {
			if err := nt.Create(repoSyncClusterRoleBinding(nn)); err != nil {
				nt.T.Fatal(err)
			}
		}

		// create namespace for namespace reconciler.
		err := nt.Create(fake.NamespaceObject(nn.Namespace))
		if err != nil {
			nt.T.Fatal(err)
		}

		// create secret for the namespace reconciler.
		CreateNamespaceSecret(nt, nn.Namespace)

		if err := setupRepoSyncRoleBinding(nt, nn); err != nil {
			nt.T.Fatal(err)
		}

		setupRepoSync(nt, nn)
	}

	// Validate multi-repo metrics in root reconcilers.
	for rsName := range opts.RootRepos {
		rootReconciler := core.RootReconcilerName(rsName)
		if err := waitForReconciler(nt, rootReconciler); err != nil {
			nt.T.Fatal(err)
		}

		err := nt.ValidateMetrics(SyncMetricsToLatestCommit(nt), func() error {
			err := nt.ValidateMultiRepoMetrics(rootReconciler, nt.DefaultRootSyncObjectCount())
			if err != nil {
				return err
			}
			return nt.ValidateErrorMetricsNotFound()
		})
		if err != nil {
			nt.T.Fatal(err)
		}
	}

	for nn := range opts.NamespaceRepos {
		nsReconciler := core.NsReconcilerName(nn.Namespace, nn.Name)
		if err := waitForReconciler(nt, nsReconciler); err != nil {
			nt.T.Fatal(err)
		}

		// Validate multi-repo metrics in namespace reconciler.
		err := nt.ValidateMetrics(SyncMetricsToLatestCommit(nt), func() error {
			return nt.ValidateMultiRepoMetrics(nsReconciler, 0)
		})
		if err != nil {
			nt.T.Fatal(err)
		}
	}
}

// RootSyncObjectV1Alpha1 returns the default RootSync object.
func RootSyncObjectV1Alpha1(name, repoURL string, sourceFormat filesystem.SourceFormat) *v1alpha1.RootSync {
	rs := fake.RootSyncObjectV1Alpha1(name)
	rs.Spec.SourceFormat = string(sourceFormat)
	rs.Spec.SourceType = string(v1beta1.GitSource)
	rs.Spec.Git = &v1alpha1.Git{
		Repo:   repoURL,
		Branch: MainBranch,
		Dir:    AcmeDir,
		Auth:   "ssh",
		SecretRef: &v1alpha1.SecretReference{
			Name: controllers.GitCredentialVolume,
		},
	}
	return rs
}

// RootSyncObjectV1Alpha1FromRootRepo returns a v1alpha1 RootSync object which
// uses a repo from nt.RootRepos.
func RootSyncObjectV1Alpha1FromRootRepo(nt *NT, name string) *v1alpha1.RootSync {
	repo, found := nt.RootRepos[name]
	if !found {
		nt.T.Fatal("nonexistent root repo: %s", name)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	sourceFormat := repo.Format
	rs := RootSyncObjectV1Alpha1(name, repoURL, sourceFormat)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// RootSyncObjectV1Beta1 returns the default RootSync object with version v1beta1.
func RootSyncObjectV1Beta1(name, repoURL string, sourceFormat filesystem.SourceFormat) *v1beta1.RootSync {
	rs := fake.RootSyncObjectV1Beta1(name)
	rs.Spec.SourceFormat = string(sourceFormat)
	rs.Spec.SourceType = string(v1beta1.GitSource)
	rs.Spec.Git = &v1beta1.Git{
		Repo:   repoURL,
		Branch: MainBranch,
		Dir:    AcmeDir,
		Auth:   "ssh",
		SecretRef: &v1beta1.SecretReference{
			Name: controllers.GitCredentialVolume,
		},
	}
	return rs
}

// RootSyncObjectV1Beta1FromRootRepo returns a v1beta1 RootSync object which
// uses a repo from nt.RootRepos.
func RootSyncObjectV1Beta1FromRootRepo(nt *NT, name string) *v1beta1.RootSync {
	repo, found := nt.RootRepos[name]
	if !found {
		nt.T.Fatal("nonexistent root repo: %s", name)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	sourceFormat := repo.Format
	rs := RootSyncObjectV1Beta1(name, repoURL, sourceFormat)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// RootSyncObjectV1Beta1FromOtherRootRepo returns a v1beta1 RootSync object which
// uses a repo from a specific nt.RootRepo.
func RootSyncObjectV1Beta1FromOtherRootRepo(nt *NT, syncName, repoName string) *v1beta1.RootSync {
	repo, found := nt.RootRepos[repoName]
	if !found {
		nt.T.Fatal("nonexistent root repo: %s", repoName)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	sourceFormat := repo.Format
	rs := RootSyncObjectV1Beta1(syncName, repoURL, sourceFormat)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// StructuredNSPath returns structured path with namespace and resourcename in repo.
func StructuredNSPath(namespace, resourceName string) string {
	return fmt.Sprintf("acme/namespaces/%s/%s.yaml", namespace, resourceName)
}

// RepoSyncObjectV1Alpha1 returns the default RepoSync object in the given namespace.
// SourceFormat for RepoSync must be Unstructured (default), so it's left unspecified.
func RepoSyncObjectV1Alpha1(nn types.NamespacedName, repoURL string) *v1alpha1.RepoSync {
	rs := fake.RepoSyncObjectV1Alpha1(nn.Namespace, nn.Name)
	rs.Spec.SourceType = string(v1beta1.GitSource)
	rs.Spec.Git = &v1alpha1.Git{
		Repo:   repoURL,
		Branch: MainBranch,
		Dir:    AcmeDir,
		Auth:   "ssh",
		SecretRef: &v1alpha1.SecretReference{
			Name: "ssh-key",
		},
	}
	return rs
}

// RepoSyncObjectV1Alpha1FromNonRootRepo returns a v1alpha1 RepoSync object which
// uses a repo from nt.NonRootRepos.
func RepoSyncObjectV1Alpha1FromNonRootRepo(nt *NT, nn types.NamespacedName) *v1alpha1.RepoSync {
	repo, found := nt.NonRootRepos[nn]
	if !found {
		nt.T.Fatal("nonexistent non-root repo: %s", nn)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	// RepoSync is always Unstructured. So ignore repo.Format.
	rs := RepoSyncObjectV1Alpha1(nn, repoURL)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// RepoSyncObjectV1Beta1 returns the default RepoSync object
// with version v1beta1 in the given namespace.
func RepoSyncObjectV1Beta1(nn types.NamespacedName, repoURL string, sourceFormat filesystem.SourceFormat) *v1beta1.RepoSync {
	rs := fake.RepoSyncObjectV1Beta1(nn.Namespace, nn.Name)
	rs.Spec.SourceFormat = string(sourceFormat)
	rs.Spec.SourceType = string(v1beta1.GitSource)
	rs.Spec.Git = &v1beta1.Git{
		Repo:   repoURL,
		Branch: MainBranch,
		Dir:    AcmeDir,
		Auth:   "ssh",
		SecretRef: &v1beta1.SecretReference{
			Name: "ssh-key",
		},
	}
	return rs
}

// RepoSyncObjectV1Beta1FromNonRootRepo returns a v1beta1 RepoSync object which
// uses a repo from nt.NonRootRepos.
func RepoSyncObjectV1Beta1FromNonRootRepo(nt *NT, nn types.NamespacedName) *v1beta1.RepoSync {
	repo, found := nt.NonRootRepos[nn]
	if !found {
		nt.T.Fatal("nonexistent non-root repo: %s", nn)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	sourceFormat := repo.Format
	rs := RepoSyncObjectV1Beta1(nn, repoURL, sourceFormat)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// RepoSyncObjectV1Beta1FromOtherRootRepo returns a v1beta1 RepoSync object which
// uses a repo from nt.RootRepos.
func RepoSyncObjectV1Beta1FromOtherRootRepo(nt *NT, nn types.NamespacedName, repoName string) *v1beta1.RepoSync {
	repo, found := nt.RootRepos[repoName]
	if !found {
		nt.T.Fatal("nonexistent root repo: %s", repoName)
	}
	repoURL := nt.GitProvider.SyncURL(repo.RemoteRepoName)
	sourceFormat := repo.Format
	rs := RepoSyncObjectV1Beta1(nn, repoURL, sourceFormat)
	if nt.DefaultReconcileTimeout != 0 {
		rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(nt.DefaultReconcileTimeout)
	}
	return rs
}

// setupCentralizedControl is a pure central-control mode.
// A default root repo (root-sync) manages all other root repos and namespace repos.
func setupCentralizedControl(nt *NT, opts *ntopts.New) {
	rsCount := 0

	// Add any RootSyncs specified by the test options
	for rsName := range opts.RootRepos {
		// The default RootSync is created manually, don't check it in the repo.
		if rsName == configsync.RootSyncName {
			continue
		}
		rs := RootSyncObjectV1Beta1FromRootRepo(nt, rsName)
		if opts.MultiRepo.ReconcileTimeout != nil {
			rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(*opts.MultiRepo.ReconcileTimeout)
		}
		nt.RootRepos[configsync.RootSyncName].Add(fmt.Sprintf("acme/namespaces/%s/%s.yaml", configsync.ControllerNamespace, rsName), rs)
		nt.RootRepos[configsync.RootSyncName].CommitAndPush("Adding RootSync: " + rsName)
	}

	if len(opts.NamespaceRepos) == 0 {
		// Wait for the RootSyncs and exit early
		nt.WaitForRepoSyncs()
		return
	}

	nt.RootRepos[configsync.RootSyncName].Add("acme/cluster/cr.yaml", RepoSyncClusterRole())

	// Use a map to record the number of RepoSync namespaces
	rsNamespaces := map[string]struct{}{}

	// Add any RepoSyncs specified by the test options
	for nn := range opts.NamespaceRepos {
		ns := nn.Namespace
		rsCount++
		rsNamespaces[ns] = struct{}{}
		nt.RootRepos[configsync.RootSyncName].Add(StructuredNSPath(ns, ns), fake.NamespaceObject(ns))
		nt.RootRepos[configsync.RootSyncName].Add(StructuredNSPath(ns, fmt.Sprintf("rb-%s", nn.Name)), RepoSyncRoleBinding(nn))
		if isPSPCluster() {
			// Add a ClusterRoleBinding so that the pods can be created
			// when the cluster has PodSecurityPolicy enabled.
			// Background: If a RoleBinding (not a ClusterRoleBinding) is used,
			// it will only grant usage for pods being run in the same namespace as the binding.
			// TODO: Remove the psp related change when Kubernetes 1.25 is
			// available on GKE.
			crb := repoSyncClusterRoleBinding(nn)
			nt.RootRepos[configsync.RootSyncName].Add(fmt.Sprintf("acme/cluster/crb-%s-%s.yaml", ns, nn.Name), crb)
		}

		rs := RepoSyncObjectV1Beta1FromNonRootRepo(nt, nn)
		if opts.MultiRepo.ReconcileTimeout != nil {
			rs.Spec.SafeOverride().ReconcileTimeout = toMetav1Duration(*opts.MultiRepo.ReconcileTimeout)
		}
		nt.RootRepos[configsync.RootSyncName].Add(StructuredNSPath(ns, nn.Name), rs)

		nt.RootRepos[configsync.RootSyncName].CommitAndPush("Adding namespace, clusterrole, rolebinding, clusterrolebinding and RepoSync")
	}

	// Convert namespace set to list
	namespaces := make([]string, 0, len(rsNamespaces))
	for namespace := range rsNamespaces {
		namespaces = append(namespaces, namespace)
	}

	// Wait for all Namespaces to be applied and reconciled.
	WaitForNamespaces(nt, nt.DefaultWaitTimeout, namespaces...)

	// Now that the Namespaces exist, create the Secrets,
	// which are required for the RepoSyncs to reconcile.
	for nn := range opts.NamespaceRepos {
		CreateNamespaceSecret(nt, nn.Namespace)
	}

	// Wait for all RootSyncs and all RepoSyncs to be reconciled
	nt.WaitForRepoSyncs()

	// Validate all RepoSyncs exist
	for nn := range opts.NamespaceRepos {
		err := nt.Validate(nn.Name, nn.Namespace, &v1beta1.RepoSync{})
		if err != nil {
			nt.T.Fatal(err)
		}
	}

	// Validate multi-repo metrics.
	err := nt.ValidateMetrics(SyncMetricsToLatestCommit(nt), func() error {
		gvkMetrics := []testmetrics.GVKMetric{
			testmetrics.ResourceCreated("Namespace"),
			testmetrics.ResourceCreated("ClusterRole"),
			testmetrics.ResourceCreated("RoleBinding"),
			testmetrics.ResourceCreated(configsync.RepoSyncKind),
		}
		if isPSPCluster() {
			gvkMetrics = append(gvkMetrics, testmetrics.ResourceCreated("ClusterRoleBinding"))
		}
		numObjects := nt.DefaultRootSyncObjectCount()
		err := nt.ValidateMultiRepoMetrics(DefaultRootReconcilerName, numObjects, gvkMetrics...)
		if err != nil {
			return err
		}
		return nt.ValidateErrorMetricsNotFound()
	})
	if err != nil {
		nt.T.Fatal(err)
	}
}

// podHasReadyCondition checks if a pod status has a READY condition.
func podHasReadyCondition(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// NewPodReady checks if the new created pods are ready.
// It also checks if the new children pods that are managed by the pods are ready.
func NewPodReady(nt *NT, labelName, currentLabel, childLabel string, oldCurrentPods, oldChildPods []corev1.Pod) error {
	if len(oldCurrentPods) == 0 {
		return nil
	}
	newPods := &corev1.PodList{}
	if err := nt.List(newPods, client.InNamespace(configmanagement.ControllerNamespace), client.MatchingLabels{labelName: currentLabel}); err != nil {
		nt.T.Fatal(err)
	}
	for _, newPod := range newPods.Items {
		for _, oldPod := range oldCurrentPods {
			if newPod.Name == oldPod.Name {
				return fmt.Errorf("old pod %s is still alive", oldPod.Name)
			}
		}
		if !podHasReadyCondition(newPod.Status.Conditions) {
			return fmt.Errorf("new pod %s is not ready yet", currentLabel)
		}
	}
	return NewPodReady(nt, labelName, childLabel, "", oldChildPods, nil)
}

// DeletePodByLabel deletes pods that have the label and waits until new pods come up.
func DeletePodByLabel(nt *NT, label, value string, waitForChildren bool) {
	oldPods := &corev1.PodList{}
	if err := nt.List(oldPods, client.InNamespace(configmanagement.ControllerNamespace), client.MatchingLabels{label: value}); err != nil {
		nt.T.Fatal(err)
	}
	oldReconcilers := &corev1.PodList{}
	if value == reconcilermanager.ManagerName && waitForChildren {
		// Get the children pods managed by the reconciler-manager.
		if err := nt.List(oldReconcilers, client.InNamespace(configmanagement.ControllerNamespace), client.MatchingLabels{label: reconcilermanager.Reconciler}); err != nil {
			nt.T.Fatal(err)
		}
	}
	if err := nt.DeleteAllOf(&corev1.Pod{}, client.InNamespace(configmanagement.ControllerNamespace), client.MatchingLabels{label: value}); err != nil {
		nt.T.Fatalf("Pod delete failed: %v", err)
	}
	Wait(nt.T, "new pods come up", nt.DefaultWaitTimeout, func() error {
		if value == reconcilermanager.ManagerName && waitForChildren {
			// Wait for both the new reconciler-manager pod and the new reconcilers are ready.
			return NewPodReady(nt, label, value, reconcilermanager.Reconciler, oldPods.Items, oldReconcilers.Items)
		}
		return NewPodReady(nt, label, value, "", oldPods.Items, nil)
	}, WaitTimeout(nt.DefaultWaitTimeout))
}

// resetRepository re-initializes an existing remote repository or creates a new remote repository.
func resetRepository(nt *NT, repoType RepoType, nn types.NamespacedName, sourceFormat filesystem.SourceFormat) *Repository {
	if repo, found := nt.RemoteRepositories[nn]; found {
		repo.ReInit(nt, sourceFormat)
		return repo
	}
	repo := NewRepository(nt, repoType, nn, sourceFormat)
	return repo
}

func resetRootRepo(nt *NT, rsName string, sourceFormat filesystem.SourceFormat) {
	rs := fake.RootSyncObjectV1Beta1(rsName)
	if err := nt.Get(rs.Name, rs.Namespace, rs); err != nil && !apierrors.IsNotFound(err) {
		nt.T.Fatal(err)
	} else {
		nt.RootRepos[rsName] = resetRepository(nt, RootRepo, RootSyncNN(rsName), sourceFormat)
		if err == nil {
			nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceFormat": "%s", "git": {"dir": "%s"}}}`, sourceFormat, AcmeDir))
		} else {
			rs.Spec.SourceFormat = string(sourceFormat)
			rs.Spec.Git = &v1beta1.Git{
				Repo:      nt.GitProvider.SyncURL(nt.RootRepos[rsName].RemoteRepoName),
				Branch:    MainBranch,
				Dir:       AcmeDir,
				Auth:      "ssh",
				SecretRef: &v1beta1.SecretReference{Name: controllers.GitCredentialVolume},
			}
			if err = nt.Create(rs); err != nil {
				nt.T.Fatal(err)
			}
		}
	}
}

// resetRootRepos resets all RootSync objects to the initial commit to delete
// managed testing resources.
//
// The spec.sourceFormat is reset to the specified format, to ensure the
// remaining objects are deleted without erroring from format mismatch.
//
// The spec.git.dir is reset to "acme", which is created by
// NewRepository and Repository.ReInit and must still exist.
//
// TODO: reset everything else, including the branch
func resetRootRepos(nt *NT, sourceFormat filesystem.SourceFormat) {
	// Reset the default root repo first so that other managed RootSyncs can be re-created and initialized.
	rootRepo := nt.RootRepos[configsync.RootSyncName]
	// Update the sourceFormat as the test case might use a different sourceFormat
	// than the one that is created initially.
	rootRepo.Format = sourceFormat
	resetRootRepo(nt, configsync.RootSyncName, sourceFormat)
	nt.WaitForSync(kinds.RootSyncV1Beta1(), configsync.RootSyncName, configsync.ControllerNamespace,
		nt.DefaultWaitTimeout, DefaultRootSha1Fn, RootSyncHasStatusSyncCommit, nil)

	for name := range nt.RootRepos {
		if name != configsync.RootSyncName {
			resetRootRepo(nt, name, nt.RootRepos[name].Format)
			nt.WaitForSync(kinds.RootSyncV1Beta1(), name, configsync.ControllerNamespace,
				nt.DefaultWaitTimeout, DefaultRootSha1Fn, RootSyncHasStatusSyncCommit, nil)
			// Now the repo is back to the initial commit, we can delete the safety check Namespace & ClusterRole
			nt.RootRepos[name].RemoveSafetyNamespace()
			nt.RootRepos[name].RemoveSafetyClusterRole()
			nt.RootRepos[name].CommitAndPush("delete the safety check Namespace & ClusterRole")
			nt.WaitForSync(kinds.RootSyncV1Beta1(), name, configsync.ControllerNamespace,
				nt.DefaultWaitTimeout, DefaultRootSha1Fn, RootSyncHasStatusSyncCommit, nil)
		}
	}
}

// resetNamespaceRepos sets the namespace repo to the initial state. That should delete all resources in the namespace.
func resetNamespaceRepos(nt *NT) {
	namespaceRepos := &v1beta1.RepoSyncList{}
	if err := nt.List(namespaceRepos); err != nil {
		nt.T.Fatal(err)
	}
	for _, nr := range namespaceRepos.Items {
		// reset the namespace repo only when it is in 'nt.NonRootRepos' (created by test).
		// This prevents from resetting an existing namespace repo from a remote git provider.
		nn := RepoSyncNN(nr.Namespace, nr.Name)
		if _, found := nt.NonRootRepos[nn]; found {
			nt.NonRootRepos[nn] = resetRepository(nt, NamespaceRepo, nn, filesystem.SourceFormatUnstructured)
			rs := &v1beta1.RepoSync{}
			if err := nt.Get(nn.Name, nn.Namespace, rs); err != nil {
				if apierrors.IsNotFound(err) {
					// The RepoSync might be declared in other namespace repos and get pruned in the reset process.
					// If that happens, re-create the object to clean up the managed testing resources.
					rs := RepoSyncObjectV1Beta1FromNonRootRepo(nt, nn)
					if err := nt.Create(rs); err != nil {
						nt.T.Fatal(err)
					}
				} else {
					nt.T.Fatal(err)
				}
			}
			nt.WaitForSync(kinds.RepoSyncV1Beta1(), nr.Name, nr.Namespace,
				nt.DefaultWaitTimeout, DefaultRepoSha1Fn, RepoSyncHasStatusSyncCommit, nil)
		}
	}
}

// deleteRootRepos deletes RootSync objects except for the default one (root-sync).
// It also deletes the safety check namespace managed by the root repo.
func deleteRootRepos(nt *NT) {
	rootSyncs := &v1beta1.RootSyncList{}
	if err := nt.List(rootSyncs); err != nil {
		nt.T.Fatal(err)
	}
	for _, rs := range rootSyncs.Items {
		// Keep the default RootSync object
		if rs.Name == configsync.RootSyncName {
			continue
		}
		if err := nt.Delete(&rs); err != nil {
			nt.T.Fatal(err)
		}
		WaitForNotFound(nt, kinds.Deployment(), core.RootReconcilerName(rs.Name), rs.Namespace)
		WaitForNotFound(nt, kinds.RootSyncV1Beta1(), rs.Name, rs.Namespace)
	}
}

// deleteNamespaceRepos deletes the repo-sync and the namespace.
func deleteNamespaceRepos(nt *NT) {
	repoSyncs := &v1beta1.RepoSyncList{}
	if err := nt.List(repoSyncs); err != nil {
		nt.T.Fatal(err)
	}

	for _, rs := range repoSyncs.Items {
		// revokeRepoSyncNamespace will delete the namespace of RepoSync, which
		// auto-deletes the resources, including RepoSync, Deployment, RoleBinding, Secret, and etc.
		revokeRepoSyncNamespace(nt, rs.Namespace)
		nn := RepoSyncNN(rs.Namespace, rs.Name)
		if isPSPCluster() {
			revokeRepoSyncClusterRoleBinding(nt, nn)
		}
	}

	rsClusterRole := RepoSyncClusterRole()
	if err := nt.Delete(rsClusterRole); err != nil && !apierrors.IsNotFound(err) {
		nt.T.Fatal(err)
	}
	WaitForNotFound(nt, kinds.ClusterRole(), rsClusterRole.Name, "")
}

// SetPolicyDir updates the root-sync object with the provided policyDir.
func SetPolicyDir(nt *NT, syncName, policyDir string) {
	nt.T.Logf("Set policyDir to %q", policyDir)
	rs := fake.RootSyncObjectV1Beta1(syncName)
	if err := nt.Get(syncName, configmanagement.ControllerNamespace, rs); err != nil {
		nt.T.Fatal(err)
	}
	if rs.Spec.Git.Dir != policyDir {
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"git": {"dir": "%s"}}}`, policyDir))
	}
}

// SetGitBranch updates the root-sync object with the provided git branch
func SetGitBranch(nt *NT, syncName, branch string) {
	nt.T.Logf("Change git branch to %q", branch)
	rs := fake.RootSyncObjectV1Beta1(syncName)
	if err := nt.Get(syncName, configmanagement.ControllerNamespace, rs); err != nil {
		nt.T.Fatal(err)
	}
	if rs.Spec.Git.Branch != branch {
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"git": {"branch": "%s"}}}`, branch))
	}
}

func toMetav1Duration(t time.Duration) *metav1.Duration {
	return &metav1.Duration{
		Duration: t,
	}
}