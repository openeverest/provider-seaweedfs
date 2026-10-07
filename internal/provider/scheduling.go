package provider

import (
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// validateSchedulingPolicy rejects topology spread constraints, which the
// seaweedfs-operator has no field for.
func validateSchedulingPolicy(component string, policy *commonv1alpha1.SchedulingPolicy) error {
	if policy != nil && policy.TopologySpreadConstraints != nil && len(*policy.TopologySpreadConstraints) > 0 {
		return fmt.Errorf("%q component: schedulingPolicy.topologySpreadConstraints is not supported", component)
	}
	return nil
}

// applyScheduling places the pods of every component. Master (raft quorum)
// and volume (data) pods require separate nodes unless the user brings their
// own affinity; filer and S3 are left to the scheduler's built-in spreading.
func applyScheduling(c *controller.Context, sw *seaweedv1.Seaweed) {
	components := c.Instance().Spec.Components
	scheduleComponent(&sw.Spec.Master.ComponentSpec, components[common.ComponentMaster].SchedulingPolicy,
		requiredHostnameAntiAffinity(c.PodLabels(common.ComponentMaster)))
	scheduleComponent(&sw.Spec.Volume.ComponentSpec, components[common.ComponentVolume].SchedulingPolicy,
		requiredHostnameAntiAffinity(c.PodLabels(common.ComponentVolume)))
	scheduleComponent(&sw.Spec.Filer.ComponentSpec, components[common.ComponentFiler].SchedulingPolicy, nil)
	scheduleComponent(&sw.Spec.S3.ComponentSpec, components[common.ComponentS3].SchedulingPolicy, nil)
}

func scheduleComponent(spec *seaweedv1.ComponentSpec, policy *commonv1alpha1.SchedulingPolicy, defaultAffinity *corev1.Affinity) {
	spec.Affinity = defaultAffinity
	if policy == nil {
		return
	}

	if policy.Affinity != nil {
		// An empty affinity opts out of the default. Leave the field unset rather
		// than applying {}: SSA would store a previously filled object as null.
		spec.Affinity = nil
		if *policy.Affinity != (corev1.Affinity{}) {
			spec.Affinity = policy.Affinity.DeepCopy()
		}
	}
	if policy.SchedulerName != "" {
		spec.SchedulerName = new(policy.SchedulerName)
	}
	spec.NodeSelector = maps.Clone(policy.NodeSelector)
	spec.Tolerations = slices.Clone(policy.Tolerations)
}

func requiredHostnameAntiAffinity(podLabels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
				TopologyKey:   corev1.LabelHostname,
			}},
		},
	}
}
