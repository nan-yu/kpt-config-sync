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

package e2e

import (
	"fmt"
	"reflect"
	"testing"

	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	nomostesting "kpt.dev/configsync/e2e/nomostest/testing"
	"kpt.dev/configsync/e2e/nomostest/testpredicates"
	"kpt.dev/configsync/e2e/nomostest/testwatcher"
	"kpt.dev/configsync/pkg/api/configsync"
	resourcegroupv1alpha1 "kpt.dev/configsync/pkg/api/kpt.dev/v1alpha1"
	"kpt.dev/configsync/pkg/applier"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/core/k8sobjects"
	"kpt.dev/configsync/pkg/kinds"
	"sigs.k8s.io/cli-utils/pkg/common"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This file includes testcases to enable status
// or disable status in the kpt applier.

func TestStatusEnabledAndDisabled(t *testing.T) {
	nt := nomostest.New(t, nomostesting.OverrideAPI, ntopts.Unstructured)
	id := applier.InventoryID(configsync.RootSyncName, configsync.ControllerNamespace)

	rootSync := k8sobjects.RootSyncObjectV1Alpha1(configsync.RootSyncName)
	// Override the statusMode for root-reconciler
	nt.MustMergePatch(rootSync, `{"spec": {"override": {"statusMode": "disabled"}}}`)
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}

	namespaceName := "status-test"
	nt.Must(nt.RootRepos[configsync.RootSyncName].Add("acme/ns.yaml", namespaceObject(namespaceName, nil)))
	nt.Must(nt.RootRepos[configsync.RootSyncName].Add("acme/cm1.yaml", k8sobjects.ConfigMapObject(core.Name("cm1"), core.Namespace(namespaceName))))
	nt.Must(nt.RootRepos[configsync.RootSyncName].CommitAndPush("Add a namespace and a configmap"))
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}

	err := nt.Watcher.WatchObject(kinds.ResourceGroup(),
		configsync.RootSyncName, configsync.ControllerNamespace,
		[]testpredicates.Predicate{
			resourceGroupHasNoStatus,
			testpredicates.HasLabel(common.InventoryLabel, id),
		},
		testwatcher.WatchTimeout(nt.DefaultWaitTimeout))
	if err != nil {
		nt.T.Fatal(err)
	}

	// Override the statusMode for root-reconciler to re-enable the status
	nt.MustMergePatch(rootSync, `{"spec": {"override": {"statusMode": "enabled"}}}`)
	if err := nt.WatchForAllSyncs(); err != nil {
		nt.T.Fatal(err)
	}

	err = nt.Watcher.WatchObject(kinds.ResourceGroup(),
		configsync.RootSyncName, configsync.ControllerNamespace,
		[]testpredicates.Predicate{
			resourceGroupHasStatus,
			testpredicates.HasLabel(common.InventoryLabel, id),
		},
		testwatcher.WatchTimeout(nt.DefaultWaitTimeout))
	if err != nil {
		nt.T.Fatal(err)
	}
}

func resourceGroupHasNoStatus(obj client.Object) error {
	if obj == nil {
		return testpredicates.ErrObjectNotFound
	}
	rg, ok := obj.(*resourcegroupv1alpha1.ResourceGroup)
	if !ok {
		return testpredicates.WrongTypeErr(obj, &resourcegroupv1alpha1.ResourceGroup{})
	}
	// We can't check that the status field is missing, because the
	// ResourceGroup object doesn't use a pointer for status.
	// But we can check that the status is empty, which is what we really care
	// about, to reduce the size of the object in etcd.
	if !reflect.ValueOf(rg.Status).IsZero() {
		return fmt.Errorf("found non-empty status in %s", core.IDOf(obj))
	}
	return nil
}

func resourceGroupHasStatus(obj client.Object) error {
	if obj == nil {
		return testpredicates.ErrObjectNotFound
	}
	rg, ok := obj.(*resourcegroupv1alpha1.ResourceGroup)
	if !ok {
		return testpredicates.WrongTypeErr(obj, &resourcegroupv1alpha1.ResourceGroup{})
	}
	// When status is enabled, the resource statuses are computed and populated.
	if len(rg.Status.ResourceStatuses) == 0 {
		return fmt.Errorf("found empty status in %s", core.IDOf(obj))
	}
	return nil
}
