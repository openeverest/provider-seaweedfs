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
	"fmt"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// pdbMaxUnavailable lets voluntary disruptions (node drains, cluster
// upgrades) take down one pod per component at a time. That keeps master raft
// quorum, and keeps every volume readable even when a replication change left
// older volumes with fewer copies than the current policy. It never blocks a
// drain outright, including for single-replica components.
var pdbMaxUnavailable = intstr.FromInt32(1)

var pdbComponents = []string{
	common.ComponentMaster,
	common.ComponentVolume,
	common.ComponentFiler,
	common.ComponentS3,
}

func pdbName(instanceName, component string) string {
	return fmt.Sprintf("%s-%s-pdb", instanceName, component)
}

// syncPodDisruptionBudgets applies one PodDisruptionBudget per component,
// owned by the Instance. seaweedfs-operator does not create any.
func syncPodDisruptionBudgets(c *controller.Context) error {
	for _, component := range pdbComponents {
		if err := c.Apply(buildPodDisruptionBudget(c, component)); err != nil {
			return fmt.Errorf("applying %s PodDisruptionBudget: %w", component, err)
		}
	}
	return nil
}

// buildPodDisruptionBudget selects the component's pods by the labels that
// labelPods puts on every pod template.
func buildPodDisruptionBudget(c *controller.Context, component string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: c.ObjectMeta(pdbName(c.Name(), component)),
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &pdbMaxUnavailable,
			Selector:       &metav1.LabelSelector{MatchLabels: c.PodLabels(component)},
		},
	}
}
