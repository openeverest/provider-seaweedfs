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

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestSyncPodDisruptionBudgets(t *testing.T) {
	components := []string{
		common.ComponentMaster, common.ComponentVolume, common.ComponentFiler, common.ComponentS3,
	}

	t.Run("one budget per component selecting its pods", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		ctx, _ := syncSeaweed(t, newSyncClient(t, instance), instance)

		for _, component := range components {
			pdb := &policyv1.PodDisruptionBudget{}
			require.NoError(t, ctx.Get(pdb, "test-instance-"+component+"-pdb"), component)

			require.Len(t, pdb.OwnerReferences, 1, component)
			assert.Equal(t, "Instance", pdb.OwnerReferences[0].Kind, component)
			assert.Equal(t, instance.Name, pdb.OwnerReferences[0].Name, component)

			require.NotNil(t, pdb.Spec.MaxUnavailable, component)
			assert.Equal(t, intstr.FromInt32(1), *pdb.Spec.MaxUnavailable, component)
			assert.Nil(t, pdb.Spec.MinAvailable, component)

			require.NotNil(t, pdb.Spec.Selector, component)
			assert.Equal(t, podLabels(instance.Name, component), pdb.Spec.Selector.MatchLabels, component)
		}
	})

	t.Run("single-replica components still get a budget that allows drains", func(t *testing.T) {
		comps := validComponents()
		for _, component := range components {
			comp := comps[component]
			comp.Replicas = pointer.ToInt32(1)
			comps[component] = comp
		}
		instance := newSyncInstance(comps)
		ctx, _ := syncSeaweed(t, newSyncClient(t, instance), instance)

		for _, component := range components {
			pdb := &policyv1.PodDisruptionBudget{}
			require.NoError(t, ctx.Get(pdb, "test-instance-"+component+"-pdb"), component)
			assert.Equal(t, intstr.FromInt32(1), *pdb.Spec.MaxUnavailable, component)
		}
	})
}
