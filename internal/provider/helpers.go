package provider

import (
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

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
