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

package webhook

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"kpt.dev/configsync/pkg/core"
	csmetadata "kpt.dev/configsync/pkg/metadata"
	"kpt.dev/configsync/pkg/testing/fake"
	"kpt.dev/configsync/pkg/testing/openapitest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setRules(rules []rbacv1.PolicyRule) core.MetaMutator {
	return func(o client.Object) {
		role := o.(*rbacv1.Role)
		role.Rules = rules
	}
}

func TestObjectDiffer_Structured(t *testing.T) {
	testCases := []struct {
		name    string
		mutsOld []core.MetaMutator
		mutsNew []core.MetaMutator
		want    string
	}{
		{
			name:    "No changes",
			mutsNew: []core.MetaMutator{},
			want:    "",
		},
		{
			name: "Add a label",
			mutsNew: []core.MetaMutator{
				core.Labels(map[string]string{
					"this": "that",
					"here": "there",
				}),
			},
			want: ".metadata.labels.here",
		},
		{
			name: "Change a label",
			mutsNew: []core.MetaMutator{
				core.Labels(map[string]string{
					"this": "is not that",
				}),
			},
			want: ".metadata.labels.this",
		},
		{
			name: "Remove a label",
			mutsNew: []core.MetaMutator{
				core.Labels(map[string]string{}),
			},
			want: ".metadata.labels\n.metadata.labels.this",
		},
		{
			name: "Add a rule",
			mutsNew: []core.MetaMutator{
				setRules([]rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"namespaces"},
						Verbs:     []string{"get", "list"},
					},
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get"},
					},
				}),
			},
			want: ".rules",
		},
		{
			name: "Change a rule",
			mutsNew: []core.MetaMutator{
				setRules([]rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"namespaces"},
						Verbs:     []string{"get", "list", "delete"},
					},
				}),
			},
			want: ".rules",
		},
		{
			name: "Remove a rule",
			mutsNew: []core.MetaMutator{
				setRules([]rbacv1.PolicyRule{}),
			},
			want: ".rules",
		},
		{
			name: "Switch the rule order",
			mutsOld: []core.MetaMutator{
				setRules([]rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get", "list"},
					},
					{
						APIGroups: []string{""},
						Resources: []string{"namespaces"},
						Verbs:     []string{"get", "list"},
					},
				}),
			},
			mutsNew: []core.MetaMutator{
				setRules([]rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"namespaces"},
						Verbs:     []string{"get", "list"},
					},
					{
						APIGroups: []string{""},
						Resources: []string{"pods"},
						Verbs:     []string{"get", "list"},
					},
				}),
			},
			// TODO(b/297234996): the two objects are semantically equal, so the diff set should be empty.
			want: ".rules",
		},
	}

	vc, err := openapitest.ValueConverterForTest()
	if err != nil {
		t.Fatalf("Failed to create ValueConverter: %v", err)
	}
	od := &ObjectDiffer{vc}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldObj := roleForTest(tc.mutsOld...)
			newObj := roleForTest(tc.mutsNew...)
			got, err := od.FieldDiff(oldObj, newObj)
			if err != nil {
				t.Errorf("Got unexpected error: %v", err)
			} else if got.String() != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func roleForTest(muts ...core.MetaMutator) *rbacv1.Role {
	role := fake.RoleObject(
		core.Name("hello"),
		core.Namespace("world"),
		core.Label("this", "that"))

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     []string{"get", "list"},
		},
	}
	for _, mut := range muts {
		mut(role)
	}
	return role
}

func TestObjectDiffer_Unstructured(t *testing.T) {
	testCases := []struct {
		name    string
		mutsOld []mutator
		mutsNew []mutator
		want    string
	}{
		{
			name:    "No changes",
			mutsNew: []mutator{},
			want:    "",
		},
		{
			name: "Add a label",
			mutsNew: []mutator{
				setLabels(t, map[string]interface{}{
					"this": "that",
					"here": "there",
				}),
			},
			want: ".metadata.labels.here",
		},
		{
			name: "Change a label",
			mutsNew: []mutator{
				setLabels(t, map[string]interface{}{
					"this": "is not that",
				}),
			},
			want: ".metadata.labels.this",
		},
		{
			name: "Remove a label",
			mutsNew: []mutator{
				setLabels(t, map[string]interface{}{}),
			},
			want: ".metadata.labels.this",
		},
		{
			name: "Add a rule",
			mutsNew: []mutator{
				setRulesUnstructured(t, []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"namespaces"},
						"verbs":     []interface{}{"get", "list"},
					},
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"pods"},
						"verbs":     []interface{}{"get", "list"},
					},
				}),
			},
			want: ".rules",
		},
		{
			name: "Change a rule",
			mutsNew: []mutator{
				setRulesUnstructured(t, []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"namespaces"},
						"verbs":     []interface{}{"get", "list", "delete"},
					},
				}),
			},
			want: ".rules",
		},
		{
			name: "Remove a rule",
			mutsOld: []mutator{
				setRulesUnstructured(t, []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"pods"},
						"verbs":     []interface{}{"get", "list"},
					},
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"namespaces"},
						"verbs":     []interface{}{"get", "list"},
					},
				}),
			},
			mutsNew: []mutator{
				setRulesUnstructured(t, []interface{}{}),
			},
			want: ".rules",
		},
		{
			name: "Switch the rule order",
			mutsOld: []mutator{
				setRulesUnstructured(t, []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"pods"},
						"verbs":     []interface{}{"get", "list"},
					},
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"namespaces"},
						"verbs":     []interface{}{"get", "list"},
					},
				}),
			},
			mutsNew: []mutator{
				setRulesUnstructured(t, []interface{}{
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"namespaces"},
						"verbs":     []interface{}{"get", "list"},
					},
					map[string]interface{}{
						"apiGroups": []interface{}{""},
						"resources": []interface{}{"pods"},
						"verbs":     []interface{}{"get", "list"},
					},
				}),
			},
			// TODO(b/297234996): the two objects are semantically equal, so the diff set should be empty.
			want: ".rules",
		},
	}

	vc, err := openapitest.ValueConverterForTest()
	if err != nil {
		t.Fatalf("Failed to create ValueConverter: %v", err)
	}
	od := &ObjectDiffer{vc}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldObj := unstructuredForTest(tc.mutsOld...)
			newObj := unstructuredForTest(tc.mutsNew...)
			got, err := od.FieldDiff(oldObj, newObj)
			if err != nil {
				t.Errorf("Got unexpected error: %v", err)
			} else if got.String() != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

type mutator func(u *unstructured.Unstructured)

func setLabels(t *testing.T, labels map[string]interface{}) mutator {
	return func(u *unstructured.Unstructured) {
		t.Helper()
		err := unstructured.SetNestedMap(u.Object, labels, "metadata", "labels")
		if err != nil {
			t.Fatal(err)
		}
	}
}

func setRulesUnstructured(t *testing.T, rules []interface{}) mutator {
	return func(u *unstructured.Unstructured) {
		t.Helper()
		err := unstructured.SetNestedSlice(u.Object, rules, "rules")
		if err != nil {
			t.Fatal(err)
		}
	}
}

func unstructuredForTest(muts ...mutator) *unstructured.Unstructured {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": rbacv1.SchemeGroupVersion.String(),
			"kind":       "Role",
			"metadata": map[string]interface{}{
				"name":      "hello",
				"namespace": "world",
				"labels": map[string]interface{}{
					"this": "that",
				},
			},
			"rules": []interface{}{
				map[string]interface{}{
					"apiGroups": []interface{}{""},
					"resources": []interface{}{"namespaces"},
					"verbs":     []interface{}{"get", "list"},
				},
			},
		},
	}
	for _, mut := range muts {
		mut(u)
	}
	return u
}

func TestDeclaredFields(t *testing.T) {
	testCases := []struct {
		name    string
		obj     client.Object
		want    string
		wantErr bool
	}{
		{
			name: "With declared fields",
			obj: roleForTest(
				core.Annotation(csmetadata.DeclaredFieldsKey, `{"f:metadata":{"f:labels":{"f:this":{}}},"f:rules":{}}`)),
			want: ".rules\n.metadata.labels.this",
		},
		{
			name:    "Missing declared fields",
			obj:     roleForTest(),
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeclaredFields(tc.obj)
			if err != nil {
				if !tc.wantErr {
					t.Errorf("Got DeclaredFields() error %v; want nil", err)
				}
			} else {
				if tc.wantErr {
					t.Error("Got DeclaredFields() nil error; want error")
				}
				if got.String() != tc.want {
					t.Errorf("got %s, want %s", got, tc.want)
				}
			}
		})
	}
}

func TestConfigSyncMetadata(t *testing.T) {
	testCases := []struct {
		name string
		obj  client.Object
		want string
	}{
		{
			name: "With metadata",
			obj: roleForTest(
				core.Annotations(map[string]string{
					"hello":                          "goodbye",
					csmetadata.ResourceManagerKey:    ":root",
					csmetadata.ResourceManagementKey: "enabled",
				}),
				core.Labels(map[string]string{
					"here":                  "there",
					csmetadata.ManagedByKey: "config-sync",
				}),
			),
			want: ".annotations.configmanagement.gke.io/managed\n.annotations.configsync.gke.io/manager\n.labels.app.kubernetes.io/managed-by",
		},
		{
			name: "Without metadata",
			obj: roleForTest(
				core.Annotations(map[string]string{
					"hello": "goodbye",
				}),
				core.Labels(map[string]string{
					"here": "there",
				}),
			),
			want: "",
		},
	}

	vc, err := openapitest.ValueConverterForTest()
	if err != nil {
		t.Fatalf("Failed to create ValueConverter: %v", err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tv, err := vc.TypedValue(tc.obj)
			if err != nil {
				t.Fatalf("Failed to get TypedValue: %v", err)
			}
			set, err := tv.ToFieldSet()
			if err != nil {
				t.Fatalf("Failed to get FieldSet: %v", err)
			}
			got := ConfigSyncMetadata(set)
			if got.String() != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
