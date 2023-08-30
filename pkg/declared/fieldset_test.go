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
	"testing"
)

func TestToFieldSet(t *testing.T) {
	testCases := []struct {
		name    string
		obj     string
		ignores []string
		want    string
	}{
		{
			name: "slash in field",
			obj:  `{"a/b":1}`,
			want: "/a~1b",
		},
		{
			name: "tilde in field",
			obj:  `{"a~b":1}`,
			want: "/a~0b",
		},
		{
			name:    "slash in ignores",
			obj:     `{"a/b":1,"a":{"b":1}}`,
			ignores: []string{"/a~1b"},
			want:    "/a/b",
		},
		{
			name:    "tilde in ignores",
			obj:     `{"a~b":1}`,
			ignores: []string{"/a~0b"},
			want:    "",
		},
		{
			name: "empty node",
			obj:  `{"a":{},"b":1}`,
			want: "/b", // Empty node IS NOT returned
		},
		{
			name: "empty list",
			obj:  `{"a":[],"b":1}`,
			want: "/a, /b", // Empty list IS returned
		},
		{
			name: "JSON node",
			obj:  `{"a":1,"b":2,"c":3}`,
			want: `/a, /b, /c`,
		},
		{
			name: "mixed with JSON list",
			obj:  `{"a":[1,2,3],"b":[4,5,6],"c":3}`,
			want: `/a, /b, /c`,
		},
		{
			name: "nested node",
			obj:  `{"a":{"a1":1,"a2":1,"a3":2}, "b":{"b1":1,"b2":{"b3":1}}}`,
			want: `/a/a1, /a/a2, /a/a3, /b/b1, /b/b2/b3`,
		},
		{
			name: "nested list",
			obj:  `{"a":[[1,2,3],2]}`,
			want: `/a`,
		},
		{
			name: "mixed and nested",
			obj:  `{"a":1,"b":["b1",{"b2":[1],"b3":2},[1,2,3]],"c":{"c1":1,"c2":[1,2],"c3":{"c4":1}},"d":[{"d1":1,"d2":2},{"d3":1,"d4":2}]}`,
			want: `/a, /b, /c/c1, /c/c2, /c/c3/c4, /d`,
		},
		{
			name:    "ignore not found",
			obj:     `{"a":1,"b":["b1",{"b2":[1],"b3":2},[1,2,3]],"c":{"c1":1,"c2":[1,2],"c3":{"c4":1}}}`,
			ignores: []string{"/x,/y,/z"},
			want:    `/a, /b, /c/c1, /c/c2, /c/c3/c4`,
		},
		{
			name:    "valid ignore",
			obj:     `{"a":1,"b":["b1",{"b2":[1],"b3":2},[1,2,3]],"c":{"c1":1,"c2":[1,2],"c3":{"c4":1}}}`,
			ignores: []string{"/a"},
			want:    `/b, /c/c1, /c/c2, /c/c3/c4`,
		},
		{
			name:    "multiple ignores",
			obj:     `{"a":1,"b":["b1",{"b2":[1],"b3":2},[1,2,3]],"c":{"c1":1,"c2":[1,2],"c3":{"c4":1}}}`,
			ignores: []string{"/b", "/c/c3/c4"},
			want:    `/a, /c/c1, /c/c2`,
		},
		{
			name:    "all ignored",
			obj:     `{"a":1,"b":["b1",{"b2":[1],"b3":2},[1,2,3]],"c":{"c1":1,"c2":[1,2],"c3":{"c4":1}}}`,
			ignores: []string{"/a", "/b", "/c/c1", "/c/c2", "/c/c3/c4"},
			want:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var src interface{}
			if err := json.Unmarshal([]byte(tc.obj), &src); err != nil {
				t.Fatal(err)
			}

			fieldSet := toFieldSet(src, tc.ignores...)
			got := PathSetToString(fieldSet)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
