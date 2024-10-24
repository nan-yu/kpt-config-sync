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

package e2e

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"kpt.dev/configsync/e2e"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/iam"
	"kpt.dev/configsync/e2e/nomostest/kustomizecomponents"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	"kpt.dev/configsync/e2e/nomostest/registryproviders"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testutils"
	"kpt.dev/configsync/e2e/nomostest/workloadidentity"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	"kpt.dev/configsync/pkg/kinds"
	"kpt.dev/configsync/pkg/reconcilermanager"
	"kpt.dev/configsync/pkg/reconcilermanager/controllers"
	"kpt.dev/configsync/pkg/testing/fake"
)

// TestWorkloadIdentity tests both GKE WI and Fleet WI for all source types.
// It has the following requirements:
// 1. GKE cluster with workload identity enabled.
// 2. The provided Google service account exists.
// 3. The cluster is registered to a fleet if testing Fleet WI.
// 4. IAM permission and IAM policy binding are created.
func TestWorkloadIdentity(t *testing.T) {
	testCases := []struct {
		name                        string
		fleetWITest                 bool
		crossProject                bool
		sourceRepo                  string
		sourceChart                 string
		sourceVersion               string
		sourceType                  v1beta1.SourceType
		gsaEmail                    string
		rootCommitFn                nomostest.Sha1Func
		testKSAMigration            bool
		requireHelmArtifactRegistry bool
	}{
		{
			name:         "Authenticate to Git repo on CSR with GKE WI",
			fleetWITest:  false,
			crossProject: false,
			sourceRepo:   csrRepo(),
			sourceType:   v1beta1.GitSource,
			gsaEmail:     gsaCSRReaderEmail(),
			rootCommitFn: nomostest.RemoteRootRepoSha1Fn,
		},
		{
			name:         "Authenticate to Git repo on CSR with Fleet WI in the same project",
			fleetWITest:  true,
			crossProject: false,
			sourceRepo:   csrRepo(),
			sourceType:   v1beta1.GitSource,
			gsaEmail:     gsaCSRReaderEmail(),
			rootCommitFn: nomostest.RemoteRootRepoSha1Fn,
		},
		{
			name:         "Authenticate to Git repo on CSR with Fleet WI across project",
			fleetWITest:  true,
			crossProject: true,
			sourceRepo:   csrRepo(),
			sourceType:   v1beta1.GitSource,
			gsaEmail:     gsaCSRReaderEmail(),
			rootCommitFn: nomostest.RemoteRootRepoSha1Fn,
		},
		{
			name:             "Authenticate to OCI image on AR with GKE WI",
			fleetWITest:      false,
			crossProject:     false,
			sourceRepo:       privateARImage(),
			sourceType:       v1beta1.OciSource,
			gsaEmail:         gsaARReaderEmail(),
			rootCommitFn:     imageDigestFuncByDigest(privateARImage()),
			testKSAMigration: true,
		},
		{
			name:         "Authenticate to OCI image on GCR with GKE WI",
			fleetWITest:  false,
			crossProject: false,
			sourceRepo:   privateGCRImage(),
			sourceType:   v1beta1.OciSource,
			gsaEmail:     gsaGCRReaderEmail(),
			rootCommitFn: imageDigestFuncByDigest(privateGCRImage()),
		},
		{
			name:             "Authenticate to OCI image on AR with Fleet WI in the same project",
			fleetWITest:      true,
			crossProject:     false,
			sourceRepo:       privateARImage(),
			sourceType:       v1beta1.OciSource,
			gsaEmail:         gsaARReaderEmail(),
			rootCommitFn:     imageDigestFuncByDigest(privateARImage()),
			testKSAMigration: true,
		},
		{
			name:         "Authenticate to OCI image on GCR with Fleet WI in the same project",
			fleetWITest:  true,
			crossProject: false,
			sourceRepo:   privateGCRImage(),
			sourceType:   v1beta1.OciSource,
			gsaEmail:     gsaGCRReaderEmail(),
			rootCommitFn: imageDigestFuncByDigest(privateGCRImage()),
		},
		{
			name:             "Authenticate to OCI image on AR with Fleet WI across project",
			fleetWITest:      true,
			crossProject:     true,
			sourceRepo:       privateARImage(),
			sourceType:       v1beta1.OciSource,
			gsaEmail:         gsaARReaderEmail(),
			rootCommitFn:     imageDigestFuncByDigest(privateARImage()),
			testKSAMigration: true,
		},
		{
			name:         "Authenticate to OCI image on GCR with Fleet WI across project",
			fleetWITest:  true,
			crossProject: true,
			sourceRepo:   privateGCRImage(),
			sourceType:   v1beta1.OciSource,
			gsaEmail:     gsaGCRReaderEmail(),
			rootCommitFn: imageDigestFuncByDigest(privateGCRImage()),
		},
		{
			name:                        "Authenticate to Helm chart on AR with GKE WI",
			fleetWITest:                 false,
			crossProject:                false,
			sourceVersion:               privateCoreDNSHelmChartVersion,
			sourceChart:                 privateCoreDNSHelmChart,
			sourceType:                  v1beta1.HelmSource,
			gsaEmail:                    gsaARReaderEmail(),
			rootCommitFn:                nomostest.HelmChartVersionShaFn(privateCoreDNSHelmChartVersion),
			testKSAMigration:            true,
			requireHelmArtifactRegistry: true,
		},
		{
			name:                        "Authenticate to Helm chart on AR with Fleet WI in the same project",
			fleetWITest:                 true,
			crossProject:                false,
			sourceVersion:               privateCoreDNSHelmChartVersion,
			sourceChart:                 privateCoreDNSHelmChart,
			sourceType:                  v1beta1.HelmSource,
			gsaEmail:                    gsaARReaderEmail(),
			rootCommitFn:                nomostest.HelmChartVersionShaFn(privateCoreDNSHelmChartVersion),
			testKSAMigration:            true,
			requireHelmArtifactRegistry: true,
		},
		{
			name:                        "Authenticate to Helm chart on AR with Fleet WI across project",
			fleetWITest:                 true,
			crossProject:                true,
			sourceVersion:               privateCoreDNSHelmChartVersion,
			sourceChart:                 privateCoreDNSHelmChart,
			sourceType:                  v1beta1.HelmSource,
			gsaEmail:                    gsaARReaderEmail(),
			rootCommitFn:                nomostest.HelmChartVersionShaFn(privateCoreDNSHelmChartVersion),
			testKSAMigration:            true,
			requireHelmArtifactRegistry: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			opts := []ntopts.Opt{ntopts.Unstructured, ntopts.RequireGKE(t)}
			if tc.requireHelmArtifactRegistry {
				opts = append(opts, ntopts.RequireHelmArtifactRegistry(t))
			}
			rs := fake.RootSyncObjectV1Beta1(configsync.RootSyncName)
			tenant := "tenant-a"
			nt := nomostest.New(t, nomostesting.WorkloadIdentity, opts...)
			if err := workloadidentity.ValidateEnabled(nt); err != nil {
				nt.T.Fatal(err)
			}

			if err := iam.ValidateServiceAccountExists(nt, tc.gsaEmail); err != nil {
				nt.T.Fatal(err)
			}

			mustConfigureMembership(nt, tc.fleetWITest, tc.crossProject)

			spec := &sourceSpec{
				sourceType:    tc.sourceType,
				sourceRepo:    tc.sourceRepo,
				sourceChart:   tc.sourceChart,
				sourceVersion: tc.sourceVersion,
				gsaEmail:      tc.gsaEmail,
				rootCommitFn:  tc.rootCommitFn,
			}
			mustConfigureRootSync(nt, rs, tenant, spec)

			ksaRef := types.NamespacedName{
				Namespace: configsync.ControllerNamespace,
				Name:      core.RootReconcilerName(rs.Name),
			}
			nt.T.Log("Validate the GSA annotation is added to the RootSync's service account")
			require.NoError(nt.T,
				nt.Watcher.WatchObject(kinds.ServiceAccount(), ksaRef.Name, ksaRef.Namespace, []testpredicates.Predicate{
					testpredicates.HasAnnotation(controllers.GCPSAAnnotationKey, tc.gsaEmail),
				}))
			if tc.fleetWITest {
				nomostest.Wait(nt.T, "wait for FWI credentials to exist", nt.DefaultWaitTimeout, func() error {
					return testutils.ReconcilerPodHasFWICredsAnnotation(nt, nomostest.DefaultRootReconcilerName, tc.gsaEmail, configsync.AuthGCPServiceAccount)
				})
			}

			if spec.sourceType == v1beta1.HelmSource {
				err := nt.WatchForAllSyncs(nomostest.WithRootSha1Func(spec.rootCommitFn),
					nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: spec.sourceChart}))
				if err != nil {
					nt.T.Fatal(err)
				}
				if err := nt.Validate(fmt.Sprintf("my-coredns-%s", spec.sourceChart), "coredns", &appsv1.Deployment{}); err != nil {
					nt.T.Error(err)
				}
			} else {
				err := nt.WatchForAllSyncs(nomostest.WithRootSha1Func(spec.rootCommitFn),
					nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: tenant}))
				if err != nil {
					nt.T.Fatal(err)
				}
				kustomizecomponents.ValidateAllTenants(nt, string(declared.RootReconciler), "../base", tenant)
			}

			// Migrate from gcpserviceaccount to k8sserviceaccount
			if tc.testKSAMigration {
				if err := migrateFromGSAtoKSA(nt, rs, ksaRef, tc.fleetWITest, spec.rootCommitFn); err != nil {
					nt.T.Fatal(err)
				}
			}
		})
	}
}

func truncateStringByLength(s string, l int) string {
	if len(s) > l {
		return s[:l]
	}
	return s
}

// migrateFromGSAtoKSA tests the scenario of migrating from impersonating a GSA
// to leveraging KSA+WI (a.k.a, BYOID/Ubermint).
func migrateFromGSAtoKSA(nt *nomostest.NT, rs *v1beta1.RootSync, ksaRef types.NamespacedName, fleetWITest bool, rootCommitFn nomostest.Sha1Func) error {
	nt.T.Log("Update RootSync auth type from gcpserviceaccount to k8sserviceaccount")
	sourceChart := ""
	if v1beta1.SourceType(rs.Spec.SourceType) == v1beta1.HelmSource {
		// Change the source repo to guarantee new resources can be reconciled with k8sserviceaccount
		nt.Must(nt.RootRepos[configsync.RootSyncName].UseHelmChart(privateSimpleHelmChart))
		chart, err := nt.BuildAndPushHelmPackage(nt.RootRepos[configsync.RootSyncName], registryproviders.HelmChartVersion(privateSimpleHelmChartVersion))

		if err != nil {
			nt.T.Fatalf("failed to push helm chart: %v", err)
		}
		nt.MustMergePatch(rs, fmt.Sprintf(`{
			"spec": {
				"helm": {
					"repo": %q,
					"chart": %q,
					"version": %q,
					"auth": "k8sserviceaccount"
				}
			}
		}`,
			nt.HelmProvider.SyncURL(chart.Name),
			chart.Name,
			chart.Version))
		rootCommitFn = nomostest.HelmChartVersionShaFn(chart.Version)
		sourceChart = chart.Name
	} else {
		// The OCI image contains 3 tenants. The RootSync is only configured to sync
		// with the `tenant-a` directory. The migration flow changes the sync
		// directory to the root directory, which also includes tenant-b and tenant-c.
		// This is to guarantee new resources can be reconciled with k8sserviceaccount.
		// Validate previously reconciled object is pruned
		if err := nt.ValidateNotFound("tenant-b", "", &corev1.Namespace{}); err != nil {
			return err
		}
		if err := nt.ValidateNotFound("tenant-c", "", &corev1.Namespace{}); err != nil {
			return err
		}
		nt.MustMergePatch(rs, `{
			"spec": {
				"oci": {
          "auth": "k8sserviceaccount",
					"dir": "."
    		}
			}
		}`)
	}

	// Validations
	nt.T.Log("Validate the GSA annotation is removed from the RootSync's service account")
	if err := nt.Watcher.WatchObject(kinds.ServiceAccount(), ksaRef.Name, ksaRef.Namespace, []testpredicates.Predicate{
		testpredicates.MissingAnnotation(controllers.GCPSAAnnotationKey),
	}); err != nil {
		return err
	}
	if fleetWITest {
		nt.T.Log("Validate the serviceaccount_impersonation_url is absent from the injected FWI credentials")
		nomostest.Wait(nt.T, "wait for FWI credentials to exist", nt.DefaultWaitTimeout, func() error {
			return testutils.ReconcilerPodHasFWICredsAnnotation(nt, nomostest.DefaultRootReconcilerName, "", configsync.AuthK8sServiceAccount)
		})
	}

	if v1beta1.SourceType(rs.Spec.SourceType) == v1beta1.HelmSource {
		if err := nt.WatchForAllSyncs(nomostest.WithRootSha1Func(rootCommitFn),
			nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: sourceChart})); err != nil {
			return err
		}
		// Validate previously reconciled object is pruned
		if err := nt.ValidateNotFound(fmt.Sprintf("my-coredns-%s", sourceChart), "coredns", &appsv1.Deployment{}); err != nil {
			return err
		}
		// Validate objects in the new helm chart are reconciled
		if err := nt.Validate("deploy-default", "default", &appsv1.Deployment{}); err != nil {
			return err
		}
		if err := nt.Validate("deploy-ns", "ns", &appsv1.Deployment{}); err != nil {
			return err
		}
	} else {
		if err := nt.WatchForAllSyncs(nomostest.WithRootSha1Func(rootCommitFn),
			nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: "."})); err != nil {
			return err
		}
		// Validate all tenants are reconciled
		kustomizecomponents.ValidateAllTenants(nt, string(declared.RootReconciler), "base", "tenant-a", "tenant-b", "tenant-c")
	}
	return nil
}

// mustConfigureMembership clears the membership before and after the test.
// When testing Fleet WI, it registers the cluster to a fleet.
func mustConfigureMembership(nt *nomostest.NT, fleetWITest, crossProject bool) {
	// Truncate the fleetMembership length to be at most 63 characters.
	fleetMembership := truncateStringByLength(fmt.Sprintf("%s-%s", truncateStringByLength(*e2e.GCPProject, 20), nt.ClusterName), 63)
	gkeURI := "https://container.googleapis.com/v1/projects/" + *e2e.GCPProject
	if *e2e.GCPRegion != "" {
		gkeURI += fmt.Sprintf("/locations/%s/clusters/%s", *e2e.GCPRegion, nt.ClusterName)
	} else {
		gkeURI += fmt.Sprintf("/zones/%s/clusters/%s", *e2e.GCPZone, nt.ClusterName)
	}

	testutils.ClearMembershipInfo(nt, fleetMembership, *e2e.GCPProject, gkeURI)
	testutils.ClearMembershipInfo(nt, fleetMembership, testutils.TestCrossProjectFleetProjectID, gkeURI)

	nt.T.Cleanup(func() {
		testutils.ClearMembershipInfo(nt, fleetMembership, *e2e.GCPProject, gkeURI)
		testutils.ClearMembershipInfo(nt, fleetMembership, testutils.TestCrossProjectFleetProjectID, gkeURI)
	})

	// Register the cluster for fleet workload identity test
	if fleetWITest {
		fleetProject := *e2e.GCPProject
		if crossProject {
			fleetProject = testutils.TestCrossProjectFleetProjectID
		}
		nt.T.Logf("Register the cluster to a fleet in project %q", fleetProject)
		if err := testutils.RegisterCluster(nt, fleetMembership, fleetProject, gkeURI); err != nil {
			nt.T.Fatalf("Failed to register the cluster to project %q: %v", fleetProject, err)
			exists, err := testutils.FleetHasMembership(nt, fleetMembership, fleetProject)
			if err != nil {
				nt.T.Fatalf("Unable to check if membership exists: %v", err)
			}
			if !exists {
				nt.T.Fatalf("The membership wasn't created")
			}
		}
		nt.T.Logf("Restart the reconciler-manager to pick up the Membership")
		// The reconciler manager checks if the Membership CRD exists before setting
		// up the RootSync and RepoSync controllers: cmd/reconciler-manager/main.go:90.
		// If the CRD exists, it configures the Membership watch.
		// Otherwise, the watch is not configured to prevent the controller from crashing caused by an unknown CRD.
		// DeletePodByLabel deletes the current reconciler-manager Pod so that new Pod
		// can set up the watch. Once the watch is configured, it can detect the
		// deletion and creation of the Membership, which implies cluster unregistration and registration.
		// The underlying reconciler should be updated with FWI creds after the reconciler-manager restarts.
		nomostest.DeletePodByLabel(nt, "app", reconcilermanager.ManagerName, false)
	}
}

type sourceSpec struct {
	sourceType    v1beta1.SourceType
	sourceRepo    string
	sourceChart   string
	sourceVersion string
	gsaEmail      string
	rootCommitFn  nomostest.Sha1Func
}

func pushSource(nt *nomostest.NT, sourceSpec *sourceSpec) error {
	// For helm charts, we need to push the chart to the AR before configuring the RootSync
	if sourceSpec.sourceType == v1beta1.HelmSource {
		nt.Must(nt.RootRepos[configsync.RootSyncName].UseHelmChart(sourceSpec.sourceChart))
		chart, err := nt.BuildAndPushHelmPackage(nt.RootRepos[configsync.RootSyncName], registryproviders.HelmChartVersion(sourceSpec.sourceVersion))
		if err != nil {
			nt.T.Fatalf("failed to push helm chart: %v", err)
		}

		sourceSpec.sourceRepo = nt.HelmProvider.SyncURL(chart.Name)
		sourceSpec.sourceChart = chart.Name
		sourceSpec.sourceVersion = chart.Version
		sourceSpec.rootCommitFn = nomostest.HelmChartVersionShaFn(chart.Version)
	}
	return nil
}

// mustConfigureRootSync updates RootSync to sync with the provided auth.
// It reuses the RootSync instead of creating a new one so that test resources
// can be cleaned up after the test.
func mustConfigureRootSync(nt *nomostest.NT, rs *v1beta1.RootSync, tenant string, sourceSpec *sourceSpec) {
	if err := pushSource(nt, sourceSpec); err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Logf("Update RootSync to sync %s from repo %s", tenant, sourceSpec.sourceRepo)
	switch sourceSpec.sourceType {
	case v1beta1.GitSource:
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"git": {"dir": "%s", "branch": "main", "repo": "%s", "auth": "gcpserviceaccount", "gcpServiceAccountEmail": "%s", "secretRef": {"name": ""}}}}`,
			tenant, sourceSpec.sourceRepo, sourceSpec.gsaEmail))
	case v1beta1.OciSource:
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "oci": {"dir": "%s", "image": "%s", "auth": "gcpserviceaccount", "gcpServiceAccountEmail": "%s"}, "git": null}}`,
			v1beta1.OciSource, tenant, sourceSpec.sourceRepo, sourceSpec.gsaEmail))
	case v1beta1.HelmSource:
		// Set the helm re-pulling duration to 5s instead of relying on the default 1h,
		// because updates to IAM policy bindings doesn't trigger a reconciliation.
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": {"chart": "%s", "repo": "%s", "version": "%s", "auth": "gcpserviceaccount", "gcpServiceAccountEmail": "%s", "releaseName": "my-coredns", "namespace": "coredns", "period": "5s"}, "git": null}}`,
			v1beta1.HelmSource, sourceSpec.sourceChart, sourceSpec.sourceRepo, sourceSpec.sourceVersion, sourceSpec.gsaEmail))
	}
}
