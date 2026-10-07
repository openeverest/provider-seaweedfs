package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestValidateSchedulingPolicy(t *testing.T) {
	require.NoError(t, validateSchedulingPolicy(common.ComponentMaster, nil))
	require.NoError(t, validateSchedulingPolicy(common.ComponentMaster, &commonv1alpha1.SchedulingPolicy{
		TopologySpreadConstraints: &[]corev1.TopologySpreadConstraint{},
	}))

	err := validateSchedulingPolicy(common.ComponentMaster, &commonv1alpha1.SchedulingPolicy{
		TopologySpreadConstraints: &[]corev1.TopologySpreadConstraint{{
			MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone, WhenUnsatisfiable: corev1.ScheduleAnyway,
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "topologySpreadConstraints is not supported")
}

func withSchedulingPolicy(component string, policy *commonv1alpha1.SchedulingPolicy) map[string]corev1alpha1.ComponentSpec {
	comps := validComponents()
	comp := comps[component]
	comp.SchedulingPolicy = policy
	comps[component] = comp
	return comps
}

func TestSyncScheduling(t *testing.T) {
	t.Run("master and volume require separate nodes by default", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		_, sw := syncSeaweed(t, newSyncClient(t, instance), instance)

		assert.Equal(t, requiredHostnameAntiAffinity(podLabels(instance.Name, common.ComponentMaster)), sw.Spec.Master.Affinity)
		assert.Equal(t, requiredHostnameAntiAffinity(podLabels(instance.Name, common.ComponentVolume)), sw.Spec.Volume.Affinity)
		assert.Nil(t, sw.Spec.Filer.Affinity)
		assert.Nil(t, sw.Spec.S3.Affinity)
	})

	t.Run("policy is passed through", func(t *testing.T) {
		userAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: "disktype", Operator: corev1.NodeSelectorOpIn, Values: []string{"ssd"},
				}}}},
			},
		}}
		tolerations := []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "storage", Effect: corev1.TaintEffectNoSchedule}}
		instance := newSyncInstance(withSchedulingPolicy(common.ComponentVolume, &commonv1alpha1.SchedulingPolicy{
			SchedulerName: "custom-scheduler",
			NodeSelector:  map[string]string{"pool": "storage"},
			Affinity:      userAffinity,
			Tolerations:   tolerations,
		}))
		_, sw := syncSeaweed(t, newSyncClient(t, instance), instance)

		assert.Equal(t, userAffinity, sw.Spec.Volume.Affinity)
		require.NotNil(t, sw.Spec.Volume.SchedulerName)
		assert.Equal(t, "custom-scheduler", *sw.Spec.Volume.SchedulerName)
		assert.Equal(t, map[string]string{"pool": "storage"}, sw.Spec.Volume.NodeSelector)
		assert.Equal(t, tolerations, sw.Spec.Volume.Tolerations)
	})

	t.Run("empty affinity opts out of the default on a live cluster", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		cl := newSyncClient(t, instance)
		_, sw := syncSeaweed(t, cl, instance)
		require.NotNil(t, sw.Spec.Master.Affinity)

		instance.Spec.Components = newSyncInstance(withSchedulingPolicy(common.ComponentMaster, &commonv1alpha1.SchedulingPolicy{
			Affinity: &corev1.Affinity{},
		})).Spec.Components
		_, sw = syncSeaweed(t, cl, instance)
		assert.Nil(t, sw.Spec.Master.Affinity)
	})
}
