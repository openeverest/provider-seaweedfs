package provider

import (
	"fmt"
	"maps"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

// OpenEverest exposes ServiceType, Annotations, and LoadBalancerService.SourceRanges.
// Only S3 supports service exposure today. SourceRanges has no operator field, so it is rejected.
func validateService(component string, svc *corev1alpha1.Service) error {
	if svc == nil {
		return nil
	}

	switch svc.ServiceType {
	case "", corev1.ServiceTypeClusterIP, corev1.ServiceTypeNodePort, corev1.ServiceTypeLoadBalancer:
		// ok - operator CRD type is an unvalidated string; we restrict here
	default:
		// ExternalName would be passed through by the operator with no ExternalName
		// hostname field, producing an invalid Service.
		return fmt.Errorf("%q component: unsupported serviceType %q (must be ClusterIP, NodePort, or LoadBalancer)", component, svc.ServiceType)
	}

	if svc.LoadBalancerService != nil {
		if svc.ServiceType != corev1.ServiceTypeLoadBalancer {
			return fmt.Errorf("%q component: loadBalancerService is only valid with serviceType LoadBalancer", component)
		}
		if len(svc.LoadBalancerService.SourceRanges) > 0 {
			return fmt.Errorf("%q component: loadBalancerService.sourceRanges is not supported", component)
		}
	}

	return nil
}

// validateNoService rejects non-default service exposure on components that
// do not support it. Only S3 can be exposed today; other components silently
// ignore Service, which would mislead users into thinking they exposed something.
func validateNoService(component string, comp corev1alpha1.ComponentSpec) error {
	if !isDefaultInClusterService(comp.Service) {
		return fmt.Errorf("%q component: service exposure is not supported, only s3 can be exposed", component)
	}
	return nil
}

// isDefaultInClusterService is true when the Instance service is unset or is
// the no-op ClusterIP default (no annotations, no loadBalancerService).
func isDefaultInClusterService(svc *corev1alpha1.Service) bool {
	if svc == nil {
		return true
	}
	if len(svc.Annotations) > 0 || svc.LoadBalancerService != nil {
		return false
	}
	return svc.ServiceType == "" || svc.ServiceType == corev1.ServiceTypeClusterIP
}

// buildServiceSpec maps OpenEverest ComponentSpec.Service onto the operator
// ServiceSpec for the S3 gateway. Returns nil for the default ClusterIP case so
// the operator keeps its own defaults.
//
// Limitation: seaweedfs-operator CreateOrUpdateService merges annotations into
// the existing Service and never deletes keys. Removing an annotation from the
// Instance (or switching LoadBalancer -> ClusterIP) can leave stale cloud LB
// annotations behind until they are cleaned up out-of-band.
// Issue tracked here: https://github.com/seaweedfs/seaweedfs-operator/issues/403
func buildServiceSpec(svc *corev1alpha1.Service) *seaweedv1.ServiceSpec {
	if isDefaultInClusterService(svc) {
		return nil
	}

	serviceType := corev1.ServiceTypeClusterIP
	if svc != nil && svc.ServiceType != "" {
		serviceType = svc.ServiceType
	}
	spec := &seaweedv1.ServiceSpec{
		Type: serviceType,
	}

	if svc != nil && len(svc.Annotations) > 0 {
		spec.Annotations = make(map[string]string, len(svc.Annotations))
		maps.Copy(spec.Annotations, svc.Annotations)
	}

	return spec
}

func buildPersistenceSpec(storage *corev1alpha1.Storage) *seaweedv1.PersistenceSpec {
	if storage == nil || storage.Size.IsZero() {
		return nil
	}

	persistence := &seaweedv1.PersistenceSpec{
		Enabled: true,
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: storage.Size,
			},
		},
	}
	if storage.StorageClass != nil && *storage.StorageClass != "" {
		persistence.StorageClassName = storage.StorageClass
	}
	return persistence
}
