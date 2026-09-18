package provider

import (
	"context"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestValidateRequiredComponent(t *testing.T) {
	tests := []struct {
		name      string
		comp      corev1alpha1.ComponentSpec
		present   bool
		expectErr string
	}{
		{
			name:      "missing component",
			present:   false,
			expectErr: "is required",
		},
		{
			name:      "missing replicas",
			comp:      corev1alpha1.ComponentSpec{},
			present:   true,
			expectErr: "replicas is required",
		},
		{
			name:      "zero replicas",
			comp:      corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(0)},
			present:   true,
			expectErr: "replicas must be at least 1",
		},
		{
			name:    "valid",
			comp:    corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			present: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequiredComponent(common.ComponentVolume, tt.comp, tt.present)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateMaster(t *testing.T) {
	tests := []struct {
		name      string
		comp      corev1alpha1.ComponentSpec
		present   bool
		expectErr string
	}{
		{
			name:      "missing master",
			present:   false,
			expectErr: "is required",
		},
		{
			name:      "even replicas",
			comp:      corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)},
			present:   true,
			expectErr: "must be odd",
		},
		{
			name:    "odd replicas",
			comp:    corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(3)},
			present: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMaster(tt.comp, tt.present)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateMasterParameters(t *testing.T) {
	require.NoError(t, validateMasterParameters(components.MasterCustomSpec{}))
	require.NoError(t, validateMasterParameters(components.MasterCustomSpec{
		MasterVolumeSizeLimitMB: pointer.ToInt32(1024),
	}))

	err := validateMasterParameters(components.MasterCustomSpec{
		MasterVolumeSizeLimitMB: pointer.ToInt32(0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "masterVolumeSizeLimitMB must be at least 1")
}

func TestValidateVolume(t *testing.T) {
	tests := []struct {
		name      string
		comp      corev1alpha1.ComponentSpec
		present   bool
		expectErr string
	}{
		{
			name:      "missing volume",
			present:   false,
			expectErr: "is required",
		},
		{
			name:      "missing storage",
			comp:      corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			present:   true,
			expectErr: "storage.size is required",
		},
		{
			name: "zero storage size",
			comp: corev1alpha1.ComponentSpec{
				Replicas: pointer.ToInt32(1),
				Storage:  &corev1alpha1.Storage{},
			},
			present:   true,
			expectErr: "storage.size is required",
		},
		{
			name: "valid",
			comp: corev1alpha1.ComponentSpec{
				Replicas: pointer.ToInt32(1),
				Storage:  &corev1alpha1.Storage{Size: resource.MustParse("10Gi")},
			},
			present: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVolume(tt.comp, tt.present)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateVolumeParameters(t *testing.T) {
	require.NoError(t, validateVolumeParameters(components.VolumeCustomSpec{}))
	require.NoError(t, validateVolumeParameters(components.VolumeCustomSpec{
		MaxVolumeCounts: pointer.ToInt32(8),
	}))

	err := validateVolumeParameters(components.VolumeCustomSpec{
		MaxVolumeCounts: pointer.ToInt32(0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxVolumeCounts must be at least 1")
}

func TestValidateTopologyParameters(t *testing.T) {
	require.NoError(t, validateTopologyParameters(standalone.StandaloneTopologyConfig{}))
	require.NoError(t, validateTopologyParameters(standalone.StandaloneTopologyConfig{
		VolumeServerDiskCount: pointer.ToInt32(2),
	}))

	err := validateTopologyParameters(standalone.StandaloneTopologyConfig{
		VolumeServerDiskCount: pointer.ToInt32(0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "volumeServerDiskCount must be at least 1")
}

func TestBuildMasterSpec(t *testing.T) {
	t.Run("defaults when no custom spec", func(t *testing.T) {
		spec := buildMasterSpec(corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(3)}, components.MasterCustomSpec{})
		require.NotNil(t, spec.VolumeSizeLimitMB)
		assert.Equal(t, DefaultMasterVolumeSizeLimitMB, *spec.VolumeSizeLimitMB)
		assert.Equal(t, int32(3), spec.Replicas)
		assert.Nil(t, spec.Persistence)
	})

	t.Run("custom volume size limit overrides default", func(t *testing.T) {
		spec := buildMasterSpec(
			corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			components.MasterCustomSpec{MasterVolumeSizeLimitMB: pointer.ToInt32(2048)},
		)
		require.NotNil(t, spec.VolumeSizeLimitMB)
		assert.Equal(t, int32(2048), *spec.VolumeSizeLimitMB)
	})

	t.Run("storage enables persistence", func(t *testing.T) {
		storageClass := "fast"
		spec := buildMasterSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Storage: &corev1alpha1.Storage{
				Size:         resource.MustParse("5Gi"),
				StorageClass: &storageClass,
			},
		}, components.MasterCustomSpec{})
		require.NotNil(t, spec.Persistence)
		assert.True(t, spec.Persistence.Enabled)
		require.NotNil(t, spec.Persistence.StorageClassName)
		assert.Equal(t, "fast", *spec.Persistence.StorageClassName)
		assert.Equal(t, resource.MustParse("5Gi"), spec.Persistence.Resources.Requests[corev1.ResourceStorage])
	})
}

func TestBuildVolumeSpec(t *testing.T) {
	t.Run("defaults when no custom spec", func(t *testing.T) {
		spec := buildVolumeSpec(corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)}, components.VolumeCustomSpec{})
		require.NotNil(t, spec.MaxVolumeCounts)
		assert.Equal(t, DefaultMaxVolumeCounts, *spec.MaxVolumeCounts)
		assert.Equal(t, int32(2), spec.Replicas)
		assert.Nil(t, spec.Requests)
		assert.Nil(t, spec.StorageClassName)
	})

	t.Run("custom max volume counts overrides default", func(t *testing.T) {
		spec := buildVolumeSpec(
			corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			components.VolumeCustomSpec{MaxVolumeCounts: pointer.ToInt32(16)},
		)
		require.NotNil(t, spec.MaxVolumeCounts)
		assert.Equal(t, int32(16), *spec.MaxVolumeCounts)
	})

	t.Run("storage is applied", func(t *testing.T) {
		storageClass := "fast"
		spec := buildVolumeSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Storage: &corev1alpha1.Storage{
				Size:         resource.MustParse("10Gi"),
				StorageClass: &storageClass,
			},
		}, components.VolumeCustomSpec{})
		assert.Equal(t, resource.MustParse("10Gi"), spec.Requests[corev1.ResourceStorage])
		require.NotNil(t, spec.StorageClassName)
		assert.Equal(t, "fast", *spec.StorageClassName)
	})

	t.Run("resources are applied", func(t *testing.T) {
		spec := buildVolumeSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"),
				},
			},
		}, components.VolumeCustomSpec{})
		assert.Equal(t, resource.MustParse("100m"), spec.Requests[corev1.ResourceCPU])
	})

	t.Run("storage merges with resource requests", func(t *testing.T) {
		spec := buildVolumeSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			Storage: &corev1alpha1.Storage{Size: resource.MustParse("10Gi")},
		}, components.VolumeCustomSpec{})
		assert.Equal(t, resource.MustParse("10Gi"), spec.Requests[corev1.ResourceStorage])
		assert.Equal(t, resource.MustParse("100m"), spec.Requests[corev1.ResourceCPU])
		assert.Equal(t, resource.MustParse("256Mi"), spec.Requests[corev1.ResourceMemory])
		assert.Equal(t, resource.MustParse("1"), spec.Limits[corev1.ResourceCPU])
	})
}

func validComponents() map[string]corev1alpha1.ComponentSpec {
	return map[string]corev1alpha1.ComponentSpec{
		common.ComponentMaster: {Replicas: pointer.ToInt32(3)},
		common.ComponentVolume: {
			Replicas: pointer.ToInt32(2),
			Storage:  &corev1alpha1.Storage{Size: resource.MustParse("10Gi")},
		},
		common.ComponentFiler: {Replicas: pointer.ToInt32(1)},
		common.ComponentS3:    {Replicas: pointer.ToInt32(1)},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		components map[string]corev1alpha1.ComponentSpec
		topology   *corev1alpha1.TopologySpec
		expectErr  string
	}{
		{
			name:       "valid",
			components: validComponents(),
		},
		{
			name: "missing master",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				delete(c, common.ComponentMaster)
				return c
			}(),
			expectErr: "is required",
		},
		{
			name: "master even replicas",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				c[common.ComponentMaster] = corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)}
				return c
			}(),
			expectErr: "must be odd",
		},
		{
			name: "missing s3",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				delete(c, common.ComponentS3)
				return c
			}(),
			expectErr: "is required",
		},
		{
			name: "invalid master parameters",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				master := c[common.ComponentMaster]
				master.Parameters = &runtime.RawExtension{Raw: []byte(`{"masterVolumeSizeLimitMB":0}`)}
				c[common.ComponentMaster] = master
				return c
			}(),
			expectErr: "masterVolumeSizeLimitMB must be at least 1",
		},
		{
			name: "missing volume storage",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				c[common.ComponentVolume] = corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)}
				return c
			}(),
			expectErr: "storage.size is required",
		},
		{
			name: "invalid volume parameters",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				volume := c[common.ComponentVolume]
				volume.Parameters = &runtime.RawExtension{Raw: []byte(`{"maxVolumeCounts":0}`)}
				c[common.ComponentVolume] = volume
				return c
			}(),
			expectErr: "maxVolumeCounts must be at least 1",
		},
		{
			name:       "invalid topology parameters",
			components: validComponents(),
			topology: &corev1alpha1.TopologySpec{
				Type:       "standalone",
				Parameters: &runtime.RawExtension{Raw: []byte(`{"volumeServerDiskCount":0}`)},
			},
			expectErr: "volumeServerDiskCount must be at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &corev1alpha1.Instance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Spec:       corev1alpha1.InstanceSpec{Components: tt.components, Topology: tt.topology},
			}
			scheme := runtime.NewScheme()
			require.NoError(t, corev1alpha1.AddToScheme(scheme))
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).Build()
			ctx := controller.NewContext(context.Background(), fakeClient, instance, common.ProviderName)

			err := New().Validate(ctx)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name        string
		seaweed     *seaweedv1.Seaweed
		expectPhase corev1alpha1.InstancePhase
	}{
		{
			name:        "cluster not found is pending",
			seaweed:     nil,
			expectPhase: corev1alpha1.InstancePhasePending,
		},
		{
			name: "ready condition true is ready",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
				},
			},
			expectPhase: corev1alpha1.InstancePhaseReady,
		},
		{
			name: "ready condition false is provisioning",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady", Message: "scaling up"}},
				},
			},
			expectPhase: corev1alpha1.InstancePhaseProvisioning,
		},
		{
			name: "no condition with replicas is provisioning",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Master: seaweedv1.ComponentStatus{Replicas: 3, ReadyReplicas: 1},
				},
			},
			expectPhase: corev1alpha1.InstancePhaseProvisioning,
		},
		{
			name: "no condition no replicas is initializing",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status:     seaweedv1.SeaweedStatus{},
			},
			expectPhase: corev1alpha1.InstancePhaseInitializing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &corev1alpha1.Instance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Spec:       corev1alpha1.InstanceSpec{Components: validComponents()},
			}
			scheme := runtime.NewScheme()
			require.NoError(t, corev1alpha1.AddToScheme(scheme))
			require.NoError(t, seaweedv1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance)
			if tt.seaweed != nil {
				builder = builder.WithObjects(tt.seaweed)
			}
			ctx := controller.NewContext(context.Background(), builder.Build(), instance, common.ProviderName)

			status, err := New().Status(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectPhase, status.Phase)
		})
	}
}
