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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/gitproviders"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/pkg/api/configmanagement"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/testing/fake"
)

func TestMultipleRemoteBranchesOutOfSync(t *testing.T) {
	nt := nomostest.New(t, nomostesting.ACMController)

	rs := fake.RootSyncObjectV1Beta1(configsync.RootSyncName)
	if err := nt.KubeClient.Get(configsync.RootSyncName, configmanagement.ControllerNamespace, rs); err != nil {
		nt.T.Fatal(err)
	}

	nt.T.Log("Create an extra remote tracking branch")
	nt.Must(nt.RootRepos[configsync.RootSyncName].Push("HEAD:refs/remotes/upstream/main"))

	nt.T.Logf("Update the remote main branch by adding a test namespace")
	nt.Must(nt.RootRepos[configsync.RootSyncName].Add("acme/namespaces/hello/ns.yaml", fake.NamespaceObject("hello")))
	nt.Must(nt.RootRepos[configsync.RootSyncName].CommitAndPush("add Namespace"))

	nt.T.Logf("Mitigation: set spec.git.branch to HEAD to pull the latest commit")
	nomostest.SetGitBranch(nt, configsync.RootSyncName, "HEAD")
	// WatchForAllSyncs validates RootSync's lastSyncedCommit is updated to the
	// local HEAD with the DefaultRootSha1Fn function.
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}
	if err := nt.Validate("hello", "", &corev1.Namespace{}); err != nil {
		nt.T.Fatal(err)
	}

	nt.T.Logf("Verify git-sync can pull the latest commit with the default branch and revision")
	nomostest.SetGitBranch(nt, configsync.RootSyncName, gitproviders.MainBranch)
	// WatchForAllSyncs validates RootSync's lastSyncedCommit is updated to the
	// local HEAD with the DefaultRootSha1Fn function.
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}

	nt.T.Logf("Remove the test namespace to make sure git-sync can fetch newer commit")
	nt.Must(nt.RootRepos[configsync.RootSyncName].Remove("acme/namespaces/hello/ns.yaml"))
	nt.Must(nt.RootRepos[configsync.RootSyncName].CommitAndPush("remove Namespace"))
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}
	if err := nt.ValidateNotFound("hello", "", &corev1.Namespace{}); err != nil {
		nt.T.Fatal(err)
	}
}
