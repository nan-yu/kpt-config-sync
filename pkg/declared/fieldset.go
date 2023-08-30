// Copyright 2023 Google LLC
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

package declared

import (
	"encoding/json"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	fieldSeparator = ", "
	slash          = "/"

	tilde       = "~"
	escapeSlash = "~1"
	escapeTilde = "~0"
)

// PathSet is a type alias of string list, which represents a set of paths.
type PathSet []string

// UnstructuredFieldSet returns the fieldSet of an unstructured object.
func UnstructuredFieldSet(un *unstructured.Unstructured, ignoreList ...string) PathSet {
	return toFieldSet(un.Object, ignoreList...)
}

// ObjectFieldSet returns the fieldSet of a typed object.
func ObjectFieldSet(obj client.Object, ignoreList ...string) (PathSet, error) {
	bytes, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	var node interface{}
	if err = json.Unmarshal(bytes, &node); err != nil {
		return nil, err
	}

	return toFieldSet(node, ignoreList...), nil
}

// toFieldSet returns a set containing every leaf field path except those in the
// ignoreList.
// The field path is in the format of JSON Pointer (RFC 6901).
// Notes:
//   - Empty node IS NOT returned as a leaf field because it is not considered as declared.
//     Adding new nested field is allowed.
//   - Empty list IS returned as a leaf field because it is declared as empty.
func toFieldSet(node any, ignoreList ...string) PathSet {
	leafPaths := map[string]struct{}{}
	traverseCurrentNode(node, slash, &leafPaths)

	var pathSet PathSet
	for _, ignore := range ignoreList {
		delete(leafPaths, ignore)
	}
	for path := range leafPaths {
		pathSet = append(pathSet, path)
	}
	SortFieldSet(pathSet)
	return pathSet
}

// SortFieldSet sorts the set so the result is stable.
func SortFieldSet(set PathSet) {
	sort.Slice(set, func(i, j int) bool {
		return strings.Compare(set[i], set[j]) < 0
	})
}

// PathSetToString serializes the PathSet into a string representation.
func PathSetToString(set PathSet) string {
	return strings.Join(set, fieldSeparator)
}

// PathSetFromString returns the PathSet from the string representation.
func PathSetFromString(s string) PathSet {
	return strings.Split(s, fieldSeparator)
}

// EscapeField uses rfc6901Escaper to escape a JSON Pointer string in compliance
// with the JavaScript Object Notation Pointer syntax: https://tools.ietf.org/html/rfc6901.
func EscapeField(key string) string {
	var rfc6901Escaper = strings.NewReplacer(tilde, escapeTilde, slash, escapeSlash)
	return rfc6901Escaper.Replace(key)
}

// UnescapeField uses rfc6901Escaper to unescape a JSON Pointer.
func UnescapeField(key string) string {
	var rfc6901Escaper = strings.NewReplacer(escapeTilde, tilde, escapeSlash, slash)
	return rfc6901Escaper.Replace(key)
}

func newPath(prefix, curPath string) string {
	if len(prefix) != 1 {
		prefix += slash
	}
	return prefix + EscapeField(curPath)
}

// traverseCurrentNode iterates each JSON node to compute the field path of each leaf node.
// Arguments:
//   - src: the current JSON node.
//   - ancestorPath: the path to the node's ancestors, e.g. "a/b/c".
//   - leafPath: the path set of all leaf nodes. It is a shared map for all recursions.
//
// Note:
//   - JSON list is considered as a leaf node
func traverseCurrentNode(node any, ancestorPath string, leafPaths *map[string]struct{}) {
	switch val := node.(type) {
	case map[string]interface{}:
		for k, v := range val {
			newPrefix := newPath(ancestorPath, k)
			traverseCurrentNode(v, newPrefix, leafPaths)
		}
	default:
		(*leafPaths)[ancestorPath] = struct{}{}
		return
	}
}
