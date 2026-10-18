// Copyright (C) 2026 The OpenEverest Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSeaweedfsVersion(t *testing.T) {
	tests := []struct {
		image  string
		expect seaweedfsVersion
		ok     bool
	}{
		{image: "chrislusf/seaweedfs:4.48", expect: seaweedfsVersion{4, 48}, ok: true},
		{image: "chrislusf/seaweedfs:4.47_large_disk", expect: seaweedfsVersion{4, 47}, ok: true},
		{image: "registry.local:5000/mirror/seaweedfs:5.1", expect: seaweedfsVersion{5, 1}, ok: true},
		{image: "chrislusf/seaweedfs:4.48@sha256:abc", expect: seaweedfsVersion{4, 48}, ok: true},
		{image: "chrislusf/seaweedfs:latest"},
		{image: "chrislusf/seaweedfs"},
		{image: "registry.local:5000/seaweedfs"},
		{image: "chrislusf/seaweedfs@sha256:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			v, ok := parseSeaweedfsVersion(tt.image)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.expect, v)
		})
	}
}

func TestValidateFilerVersion(t *testing.T) {
	tests := []struct {
		name      string
		image     string
		filers    int32
		expectErr bool
	}{
		{name: "one filer on 4.47", image: "chrislusf/seaweedfs:4.47", filers: 1},
		{name: "two filers on 4.47", image: "chrislusf/seaweedfs:4.47", filers: 2, expectErr: true},
		{name: "three filers on 3.99", image: "chrislusf/seaweedfs:3.99", filers: 3, expectErr: true},
		{name: "two filers on 4.48", image: "chrislusf/seaweedfs:4.48", filers: 2},
		{name: "two filers on 5.0", image: "chrislusf/seaweedfs:5.0", filers: 2},
		{name: "two filers on unknown tag", image: "chrislusf/seaweedfs:dev", filers: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilerVersion(tt.image, tt.filers)
			if tt.expectErr {
				assert.ErrorContains(t, err, "require SeaweedFS 4.48 or newer")
				return
			}
			assert.NoError(t, err)
		})
	}
}
