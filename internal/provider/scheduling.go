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

// applyScheduling places the pods of every component. Master, volume, and
// filer pods prefer separate nodes unless the user brings their own affinity.
// S3 is left to the scheduler. Values are written on each component. The
// operator applies a component value over the cluster one, nodeSelector is
// merged, with the component key winning.
func applyScheduling(c *controller.Context, sw *seaweedv1.Seaweed) {
	components := c.Instance().Spec.Components
	scheduleComponent(&sw.Spec.Master.ComponentSpec, components[common.ComponentMaster].SchedulingPolicy,
		preferredHostnameAntiAffinity(c.PodLabels(common.ComponentMaster)))
	scheduleComponent(&sw.Spec.Volume.ComponentSpec, components[common.ComponentVolume].SchedulingPolicy,
		preferredHostnameAntiAffinity(c.PodLabels(common.ComponentVolume)))
	scheduleComponent(&sw.Spec.Filer.ComponentSpec, components[common.ComponentFiler].SchedulingPolicy,
		preferredHostnameAntiAffinity(c.PodLabels(common.ComponentFiler)))
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

// preferredAntiAffinityWeight is a strong preference (1–100). The operator
// does not add its own anti-affinity for master, volume, or filer.
const preferredAntiAffinityWeight = 100

func preferredHostnameAntiAffinity(podLabels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: preferredAntiAffinityWeight,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
					TopologyKey:   corev1.LabelHostname,
				},
			}},
		},
	}
}
