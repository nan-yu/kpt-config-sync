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
	"fmt"
	"strconv"
	"strings"

	"github.com/wI2L/jsondiff"
	"kpt.dev/configsync/pkg/core"
	"kpt.dev/configsync/pkg/declared"
	csmetadata "kpt.dev/configsync/pkg/metadata"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	metadataAnnotations = "/metadata/annotations/"
	metadataLabels      = "/metadata/labels/"
)

// FieldDiff returns a Set of the Object fields which are being modified
// in the given Request that are also marked as fields declared in Git.
func FieldDiff(oldObj, newObj client.Object) (declared.PathSet, error) {
	patch, err := jsondiff.Compare(oldObj, newObj, jsondiff.Equivalent())
	if err != nil {
		return nil, err
	}
	pathMap := map[string]struct{}{}
	for _, op := range patch {
		switch op.Type {
		// Ideally we don't care about fields that are added since they will never
		// overlap with declared fields.
		// It checks the added fields here to validate no ConfigSync metadata is declared.
		case jsondiff.OperationAdd, jsondiff.OperationReplace:
			pathMap[stripListIndex(op.Path)] = struct{}{}
		case jsondiff.OperationRemove:
			switch oldVal := op.OldValue.(type) {
			case map[string]interface{}:
				for k := range oldVal {
					pathMap[stripListIndex(op.Path+"/"+k)] = struct{}{}
				}
			default:
				pathMap[stripListIndex(op.Path)] = struct{}{}
			}
		}
	}

	var pathSet declared.PathSet
	for path := range pathMap {
		pathSet = append(pathSet, path)
	}
	declared.SortFieldSet(pathSet)
	return pathSet, nil
}

// stripListIndex removes the List index from the provided path.
//   - If the path contains a List field (with index or '-'), it removes
//     everything after the index or '-'.
//   - If the path doesn't contain a List field, it remains unchanged.
func stripListIndex(path string) string {
	var newPath []string
	paths := strings.Split(path, "/")
	for _, p := range paths {
		if p == "-" {
			return strings.Join(newPath, "/")
		}
		if _, err := strconv.Atoi(p); err == nil {
			return strings.Join(newPath, "/")
		}
		newPath = append(newPath, p)
	}
	return path
}

// ConfigSyncMetadata returns all of the metadata fields in the given fieldpath
// Set which are ConfigSync labels or annotations.
func ConfigSyncMetadata(set declared.PathSet) declared.PathSet {
	var csSet declared.PathSet
	for _, path := range set {
		if strings.HasPrefix(path, metadataAnnotations) {
			annotation := strings.TrimPrefix(path, metadataAnnotations)
			unescaped := declared.UnescapeField(annotation)
			if csmetadata.IsConfigSyncAnnotationKey(unescaped) {
				csSet = append(csSet, path)
			}
		}
		if strings.HasPrefix(path, metadataLabels) {
			label := strings.TrimPrefix(path, metadataLabels)
			unescaped := declared.UnescapeField(label)
			if csmetadata.IsConfigSyncLabelKey(unescaped) {
				csSet = append(csSet, path)
			}
		}
	}
	return csSet
}

// DeclaredFields returns the declared fields for the given Object.
func DeclaredFields(obj client.Object) (declared.PathSet, error) {
	decls, ok := obj.GetAnnotations()[csmetadata.DeclaredFieldsKey]
	if !ok {
		return nil, fmt.Errorf("%s annotation is missing from %s", csmetadata.DeclaredFieldsKey, core.GKNN(obj))
	}
	return declared.PathSetFromString(decls), nil
}

// intersect returns a Set containing paths which appear in both set1 and set2.
func intersect(set1, set2 declared.PathSet) declared.PathSet {
	var intersection declared.PathSet
	for _, p1 := range set1 {
		for _, p2 := range set2 {
			if p1 == p2 {
				intersection = append(intersection, p1)
			}
		}
	}
	return intersection
}
