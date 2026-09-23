package provider

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

const (
	// Default S3 HTTP port used by seaweedfs-operator when Spec.S3.Port is unset.
	defaultS3Port = 8333

	// Default region for S3-compatible clients. SeaweedFS ignores it, but
	// OpenEverest BackupStorage and AWS SDKs require a non-empty value.
	defaultS3Region = "us-east-1"
)

func s3ServiceName(instanceName string) string {
	return instanceName + "-s3"
}

func buildConnectionDetailsFromService(c *controller.Context, svc *corev1.Service) controller.ConnectionDetails {
	host := fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace)
	port := serviceS3Port(svc)
	portStr := fmt.Sprintf("%d", port)
	endpoint := fmt.Sprintf("http://%s:%d", host, port)

	return controller.ConnectionDetails{
		Type:     "s3",
		Provider: common.ProviderName,
		Host:     host,
		Port:     portStr,
		URI:      endpoint,
		AdditionalProperties: map[string]string{
			"endpointURL":    endpoint,
			"forcePathStyle": "true",
			"verifyTLS":      "false",
			"region":         defaultS3Region,
		},
	}
}

func serviceS3Port(svc *corev1.Service) int32 {
	for _, p := range svc.Spec.Ports {
		if p.Name == "s3-http" || p.Port == defaultS3Port {
			return p.Port
		}
	}
	if len(svc.Spec.Ports) > 0 {
		return svc.Spec.Ports[0].Port
	}
	return defaultS3Port
}
