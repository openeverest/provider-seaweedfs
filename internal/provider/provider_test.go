package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		{
			name: "rejects non-default service",
			comp: corev1alpha1.ComponentSpec{
				Replicas: pointer.ToInt32(1),
				Service:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort},
			},
			present:   true,
			expectErr: "service exposure is not supported",
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
		{
			name: "rejects non-default service",
			comp: corev1alpha1.ComponentSpec{
				Replicas: pointer.ToInt32(1),
				Storage:  &corev1alpha1.Storage{Size: resource.MustParse("10Gi")},
				Service:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeLoadBalancer},
			},
			present:   true,
			expectErr: "service exposure is not supported",
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

func TestValidateService(t *testing.T) {
	tests := []struct {
		name      string
		svc       *corev1alpha1.Service
		expectErr string
	}{
		{name: "nil service"},
		{name: "empty service", svc: &corev1alpha1.Service{}},
		{
			name: "ClusterIP",
			svc:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeClusterIP},
		},
		{
			name: "NodePort",
			svc:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort},
		},
		{
			name: "LoadBalancer",
			svc:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeLoadBalancer},
		},
		{
			name: "LoadBalancer with empty loadBalancerService",
			svc: &corev1alpha1.Service{
				ServiceType:         corev1.ServiceTypeLoadBalancer,
				LoadBalancerService: &corev1alpha1.LoadBalancerService{},
			},
		},
		{
			name: "loadBalancerService with empty serviceType",
			svc: &corev1alpha1.Service{
				LoadBalancerService: &corev1alpha1.LoadBalancerService{},
			},
			expectErr: "loadBalancerService is only valid with serviceType LoadBalancer",
		},
		{
			name: "ExternalName unsupported",
			svc: &corev1alpha1.Service{
				ServiceType: corev1.ServiceTypeExternalName,
			},
			expectErr: "unsupported serviceType",
		},
		{
			name: "loadBalancerService without LoadBalancer type",
			svc: &corev1alpha1.Service{
				ServiceType:         corev1.ServiceTypeClusterIP,
				LoadBalancerService: &corev1alpha1.LoadBalancerService{},
			},
			expectErr: "loadBalancerService is only valid with serviceType LoadBalancer",
		},
		{
			name: "sourceRanges unsupported",
			svc: &corev1alpha1.Service{
				ServiceType: corev1.ServiceTypeLoadBalancer,
				LoadBalancerService: &corev1alpha1.LoadBalancerService{
					SourceRanges: corev1alpha1.SourceRanges{"10.0.0.0/8"},
				},
			},
			expectErr: "sourceRanges is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateService(common.ComponentS3, tt.svc)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestBuildServiceSpec(t *testing.T) {
	t.Run("nil service leaves operator defaults", func(t *testing.T) {
		assert.Nil(t, buildServiceSpec(nil))
	})

	t.Run("default ClusterIP leaves operator defaults", func(t *testing.T) {
		assert.Nil(t, buildServiceSpec(&corev1alpha1.Service{}))
		assert.Nil(t, buildServiceSpec(&corev1alpha1.Service{ServiceType: corev1.ServiceTypeClusterIP}))
	})

	t.Run("ClusterIP with annotations is applied", func(t *testing.T) {
		spec := buildServiceSpec(&corev1alpha1.Service{
			ServiceType: corev1.ServiceTypeClusterIP,
			Annotations: map[string]string{"example.com/owner": "team-a"},
		})
		require.NotNil(t, spec)
		assert.Equal(t, corev1.ServiceTypeClusterIP, spec.Type)
		assert.Equal(t, map[string]string{"example.com/owner": "team-a"}, spec.Annotations)
	})

	t.Run("NodePort", func(t *testing.T) {
		spec := buildServiceSpec(&corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort})
		require.NotNil(t, spec)
		assert.Equal(t, corev1.ServiceTypeNodePort, spec.Type)
		assert.Nil(t, spec.Annotations)
	})

	t.Run("LoadBalancer with annotations", func(t *testing.T) {
		spec := buildServiceSpec(&corev1alpha1.Service{
			ServiceType: corev1.ServiceTypeLoadBalancer,
			Annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
		})
		require.NotNil(t, spec)
		assert.Equal(t, corev1.ServiceTypeLoadBalancer, spec.Type)
		require.Equal(t, map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"}, spec.Annotations)
	})

	t.Run("LoadBalancer with empty loadBalancerService still sets Type", func(t *testing.T) {
		spec := buildServiceSpec(&corev1alpha1.Service{
			ServiceType:         corev1.ServiceTypeLoadBalancer,
			LoadBalancerService: &corev1alpha1.LoadBalancerService{},
		})
		require.NotNil(t, spec)
		assert.Equal(t, corev1.ServiceTypeLoadBalancer, spec.Type)
	})
}

func TestBuildMasterSpec(t *testing.T) {
	t.Run("defaults when no custom spec", func(t *testing.T) {
		spec := buildMasterSpec(corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(3)}, components.MasterCustomSpec{})
		require.NotNil(t, spec.VolumeSizeLimitMB)
		assert.Equal(t, DefaultMasterVolumeSizeLimitMB, *spec.VolumeSizeLimitMB)
		assert.Equal(t, int32(3), spec.Replicas)
		assert.Nil(t, spec.Persistence)
		assert.Nil(t, spec.Service)
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

func TestValidateFiler(t *testing.T) {
	tests := []struct {
		name      string
		comp      corev1alpha1.ComponentSpec
		present   bool
		expectErr string
	}{
		{
			name:      "missing filer",
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
				Storage:  &corev1alpha1.Storage{Size: resource.MustParse("1Gi")},
			},
			present: true,
		},
		{
			name: "rejects non-default service",
			comp: corev1alpha1.ComponentSpec{
				Replicas: pointer.ToInt32(1),
				Storage:  &corev1alpha1.Storage{Size: resource.MustParse("1Gi")},
				Service:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort},
			},
			present:   true,
			expectErr: "service exposure is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFiler(tt.comp, tt.present)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateFilerParameters(t *testing.T) {
	require.NoError(t, validateFilerParameters(components.FilerCustomSpec{}))
	require.NoError(t, validateFilerParameters(components.FilerCustomSpec{
		MaxMB: pointer.ToInt32(8),
	}))

	err := validateFilerParameters(components.FilerCustomSpec{
		MaxMB: pointer.ToInt32(0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxMB must be at least 1")
}

func TestValidateS3Parameters(t *testing.T) {
	require.NoError(t, validateS3Parameters(components.S3CustomSpec{}))
	require.NoError(t, validateS3Parameters(components.S3CustomSpec{
		Port:       pointer.ToInt32(8333),
		DomainName: pointer.To("s3.example.com"),
	}))
	require.NoError(t, validateS3Parameters(components.S3CustomSpec{
		Port: pointer.ToInt32(MaxS3Port),
	}))
	require.NoError(t, validateS3Parameters(components.S3CustomSpec{
		DomainName: pointer.To(""),
	}))
	require.NoError(t, validateS3Parameters(components.S3CustomSpec{
		DomainName: pointer.To("s3.example.com,cdn.example.com"),
	}))

	err := validateS3Parameters(components.S3CustomSpec{
		Port: pointer.ToInt32(0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("port must be between 1 and %d", MaxS3Port))

	err = validateS3Parameters(components.S3CustomSpec{
		Port: pointer.ToInt32(MaxS3Port + 1),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("port must be between 1 and %d", MaxS3Port))

	err = validateS3Parameters(components.S3CustomSpec{
		DomainName: pointer.To("x;id"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domainName")

	err = validateS3Parameters(components.S3CustomSpec{
		DomainName: pointer.To("s3.example.com,$(id)"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domainName")
}

func TestBuildS3Spec(t *testing.T) {
	t.Run("replicas only", func(t *testing.T) {
		spec := buildS3Spec(corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)}, components.S3CustomSpec{})
		assert.Equal(t, int32(2), spec.Replicas)
		assert.Nil(t, spec.Port)
		assert.Nil(t, spec.DomainName)
		assert.Nil(t, spec.Requests)
		assert.Nil(t, spec.Limits)
		assert.Nil(t, spec.Service)
	})

	t.Run("service type from component", func(t *testing.T) {
		spec := buildS3Spec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Service:  &corev1alpha1.Service{ServiceType: corev1.ServiceTypeLoadBalancer},
		}, components.S3CustomSpec{})
		require.NotNil(t, spec.Service)
		assert.Equal(t, corev1.ServiceTypeLoadBalancer, spec.Service.Type)
		assert.Nil(t, spec.Service.Annotations)
	})

	t.Run("custom port and domainName are applied", func(t *testing.T) {
		spec := buildS3Spec(
			corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			components.S3CustomSpec{
				Port:       pointer.ToInt32(9000),
				DomainName: pointer.To("s3.example.com"),
			},
		)
		require.NotNil(t, spec.Port)
		assert.Equal(t, int32(9000), *spec.Port)
		require.NotNil(t, spec.DomainName)
		assert.Equal(t, "s3.example.com", *spec.DomainName)
	})

	t.Run("resources are applied", func(t *testing.T) {
		spec := buildS3Spec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		}, components.S3CustomSpec{})
		assert.Equal(t, resource.MustParse("100m"), spec.Requests[corev1.ResourceCPU])
		assert.Equal(t, resource.MustParse("512Mi"), spec.Limits[corev1.ResourceMemory])
	})
}

func TestBuildFilerSpec(t *testing.T) {
	t.Run("replicas only", func(t *testing.T) {
		spec := buildFilerSpec(corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(2)}, components.FilerCustomSpec{})
		assert.Equal(t, int32(2), spec.Replicas)
		assert.Nil(t, spec.Persistence)
		assert.Nil(t, spec.MaxMB)
		assert.Nil(t, spec.Service)
	})

	t.Run("custom maxMB is applied", func(t *testing.T) {
		spec := buildFilerSpec(
			corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)},
			components.FilerCustomSpec{MaxMB: pointer.ToInt32(16)},
		)
		require.NotNil(t, spec.MaxMB)
		assert.Equal(t, int32(16), *spec.MaxMB)
	})

	t.Run("resources are applied", func(t *testing.T) {
		spec := buildFilerSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		}, components.FilerCustomSpec{})
		assert.Equal(t, resource.MustParse("100m"), spec.Requests[corev1.ResourceCPU])
		assert.Equal(t, resource.MustParse("512Mi"), spec.Limits[corev1.ResourceMemory])
	})

	t.Run("storage enables persistence", func(t *testing.T) {
		storageClass := "fast"
		spec := buildFilerSpec(corev1alpha1.ComponentSpec{
			Replicas: pointer.ToInt32(1),
			Storage: &corev1alpha1.Storage{
				Size:         resource.MustParse("1Gi"),
				StorageClass: &storageClass,
			},
		}, components.FilerCustomSpec{})
		require.NotNil(t, spec.Persistence)
		assert.True(t, spec.Persistence.Enabled)
		require.NotNil(t, spec.Persistence.StorageClassName)
		assert.Equal(t, "fast", *spec.Persistence.StorageClassName)
		assert.Equal(t, resource.MustParse("1Gi"), spec.Persistence.Resources.Requests[corev1.ResourceStorage])
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
		assert.Nil(t, spec.Service)
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
		common.ComponentFiler: {
			Replicas: pointer.ToInt32(1),
			Storage:  &corev1alpha1.Storage{Size: resource.MustParse("1Gi")},
		},
		common.ComponentS3: {Replicas: pointer.ToInt32(1)},
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
			name: "master with exposed service rejected",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				master := c[common.ComponentMaster]
				master.Service = &corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort}
				c[common.ComponentMaster] = master
				return c
			}(),
			expectErr: "service exposure is not supported",
		},
		{
			name: "s3 with NodePort allowed",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				s3 := c[common.ComponentS3]
				s3.Service = &corev1alpha1.Service{ServiceType: corev1.ServiceTypeNodePort}
				c[common.ComponentS3] = s3
				return c
			}(),
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
			name: "missing filer storage",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				c[common.ComponentFiler] = corev1alpha1.ComponentSpec{Replicas: pointer.ToInt32(1)}
				return c
			}(),
			expectErr: "storage.size is required",
		},
		{
			name: "invalid filer parameters",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				filer := c[common.ComponentFiler]
				filer.Parameters = &runtime.RawExtension{Raw: []byte(`{"maxMB":0}`)}
				c[common.ComponentFiler] = filer
				return c
			}(),
			expectErr: "maxMB must be at least 1",
		},
		{
			name: "invalid s3 parameters",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				s3 := c[common.ComponentS3]
				s3.Parameters = &runtime.RawExtension{Raw: []byte(`{"port":0}`)}
				c[common.ComponentS3] = s3
				return c
			}(),
			expectErr: fmt.Sprintf("port must be between 1 and %d", MaxS3Port),
		},
		{
			name: "invalid s3 domainName",
			components: func() map[string]corev1alpha1.ComponentSpec {
				c := validComponents()
				s3 := c[common.ComponentS3]
				s3.Parameters = &runtime.RawExtension{Raw: []byte(`{"domainName":"x;id"}`)}
				c[common.ComponentS3] = s3
				return c
			}(),
			expectErr: "domainName",
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
	s3Service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
		},
	}

	tests := []struct {
		name               string
		seaweed            *seaweedv1.Seaweed
		service            *corev1.Service
		expectPhase        corev1alpha1.InstancePhase
		expectConnection   bool
		expectExternalURL  string
	}{
		{
			name:        "cluster not found is pending",
			seaweed:     nil,
			expectPhase: corev1alpha1.InstancePhasePending,
		},
		{
			name: "ready with S3 Service publishes connection details",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
				},
			},
			service:          s3Service,
			expectPhase:      corev1alpha1.InstancePhaseReady,
			expectConnection: true,
		},
		{
			name: "ready LoadBalancer without ingress stays provisioning",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
				},
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3", Namespace: "default"},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeLoadBalancer,
					Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
				},
			},
			expectPhase: corev1alpha1.InstancePhaseProvisioning,
		},
		{
			name: "ready LoadBalancer with ingress publishes external endpoint",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
				},
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance-s3", Namespace: "default"},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeLoadBalancer,
					Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
					},
				},
			},
			expectPhase:       corev1alpha1.InstancePhaseReady,
			expectConnection:  true,
			expectExternalURL: "http://lb.example.com:8333",
		},
		{
			name: "ready without S3 Service stays provisioning",
			seaweed: &seaweedv1.Seaweed{
				ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
				Status: seaweedv1.SeaweedStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
				},
			},
			expectPhase: corev1alpha1.InstancePhaseProvisioning,
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
			require.NoError(t, corev1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance)
			if tt.seaweed != nil {
				builder = builder.WithObjects(tt.seaweed)
			}
			if tt.service != nil {
				builder = builder.WithObjects(tt.service)
			}
			ctx := controller.NewContext(context.Background(), builder.Build(), instance, common.ProviderName)

			status, err := New().Status(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectPhase, status.Phase)
			if tt.expectConnection {
				assert.Equal(t, "s3", status.ConnectionDetails.Type)
				assert.Equal(t, common.ProviderName, status.ConnectionDetails.Provider)
				assert.Equal(t, "test-instance-s3.default.svc", status.ConnectionDetails.Host)
				assert.Equal(t, "8333", status.ConnectionDetails.Port)
				assert.Equal(t, "http://test-instance-s3.default.svc:8333", status.ConnectionDetails.URI)
				assert.Equal(t, "true", status.ConnectionDetails.AdditionalProperties["forcePathStyle"])
				if tt.expectExternalURL != "" {
					assert.Equal(t, tt.expectExternalURL, status.ConnectionDetails.AdditionalProperties["externalEndpointURL"])
				} else {
					assert.NotContains(t, status.ConnectionDetails.AdditionalProperties, "externalEndpointURL")
				}
			}
		})
	}
}

func TestS3ServiceToInstance(t *testing.T) {
	seaweedOwner := func(name string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{
			APIVersion: seaweedv1.GroupVersion.String(),
			Kind:       "Seaweed",
			Name:       name,
			Controller: pointer.ToBool(true),
		}}
	}

	tests := []struct {
		name   string
		svc    *corev1.Service
		expect []reconcile.Request
	}{
		{
			name: "S3 Service owned by Seaweed enqueues Instance",
			svc: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-s3", Namespace: "ns", OwnerReferences: seaweedOwner("sw"),
			}},
			expect: []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "sw"}}},
		},
		{
			name: "non-S3 Service owned by Seaweed is ignored",
			svc: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-master", Namespace: "ns", OwnerReferences: seaweedOwner("sw"),
			}},
		},
		{
			name: "Service without controller owner is ignored",
			svc:  &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"}},
		},
		{
			name: "Service owned by another kind is ignored",
			svc: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: "sw-s3", Namespace: "ns",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "sw", Controller: pointer.ToBool(true),
				}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, s3ServiceToInstance(context.Background(), tt.svc))
		})
	}
}
