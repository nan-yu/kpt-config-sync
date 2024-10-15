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

package util

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/compute/metadata"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	hubv1 "kpt.dev/configsync/pkg/api/hub/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetProjectID(ctx context.Context, c client.Client) (string, error) {
	memberships := &hubv1.MembershipList{}
	if err := c.List(ctx, memberships); err != nil {
		if !apimeta.IsNoMatchError(err) {
			return "", fmt.Errorf("getting project ID: %v", err)
		}
	}
	if len(memberships.Items) > 1 {
		return "", fmt.Errorf("no more than one Membership is allowed, but got %d", len(memberships.Items))
	}
	if len(memberships.Items) == 1 {
		membership := memberships.Items[0]
		wiPool := membership.Spec.WorkloadIdentityPool // workload_identity_pool is of the form proj-id.svc.id.goog.
		return strings.Split(wiPool, ".")[0], nil      // ProjectID cannot have dots.
	}
	// The cluster is not registered in a fleet, so no membership exists.
	// Get the project ID from the GCE metadata server.
	if metadata.OnGCE() {
		return metadata.ProjectID()
	}
	return "", fmt.Errorf("failed to get the project ID from fleet membership or GCE metadata server")
}
