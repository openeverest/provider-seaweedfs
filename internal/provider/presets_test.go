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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// TestChartPresetsAreValid runs every ClusterInstancePreset the chart ships
// through the checks an Instance created from it gets.
func TestChartPresetsAreValid(t *testing.T) {
	data, err := os.ReadFile("../../charts/provider-seaweedfs/values.yaml")
	require.NoError(t, err)
	var values struct {
		Presets []struct {
			Name string                    `json:"name"`
			Spec corev1alpha1.InstanceSpec `json:"spec"`
		} `json:"presets"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))
	require.NotEmpty(t, values.Presets)

	v := validationtest.New(t, New(), providerSpecPath)
	for _, preset := range values.Presets {
		t.Run(preset.Name, func(t *testing.T) {
			spec := preset.Spec
			spec.ProviderRef = commonv1alpha1.ObjectRef{Name: common.ProviderName}
			in := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "from-preset"}, Spec: spec}
			res := v.Run(in, nil)
			assert.Empty(t, res.Errors)
			assert.Empty(t, res.Warnings)
		})
	}
}
