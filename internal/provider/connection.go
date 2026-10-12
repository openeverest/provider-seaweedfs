package provider

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

const (
	// Default S3 HTTP port used by seaweedfs-operator when Spec.S3.Port is unset.
	defaultS3Port = 8333

	// Name of the S3 HTTP port on the operator-managed S3 Service.
	s3HTTPPortName = "s3-http"

	// Default region for S3-compatible clients. SeaweedFS ignores it, but
	// OpenEverest BackupStorage and AWS SDKs require a non-empty value.
	defaultS3Region = "us-east-1"
)

func s3ServiceName(instanceName string) string {
	return instanceName + "-s3"
}

func s3IngressName(instanceName string) string {
	return instanceName + "-s3-ingress"
}

func buildConnectionDetailsFromService(
	c *controller.Context,
	svc *corev1.Service,
	sw *seaweedv1.Seaweed,
	s3Params components.S3CustomSpec,
) (controller.ConnectionDetails, error) {
	host := fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace)
	port := serviceS3Port(svc)
	portStr := fmt.Sprintf("%d", port)
	// In-cluster endpoint stays HTTP: the S3 Service never speaks TLS. BackupStorage
	// and in-cluster clients should use this URL.
	endpoint := fmt.Sprintf("http://%s:%d", host, port)

	props := map[string]string{
		"endpointURL":    endpoint,
		"forcePathStyle": "true",
		"verifyTLS":      "false",
		"region":         defaultS3Region,
	}

	// Host stays in-cluster (BackupStorage needs it). For NodePort/LoadBalancer,
	// also surface an external endpoint so clients outside the cluster can connect.
	externalURL, err := externalS3EndpointURL(c, svc, port, sw)
	if err != nil {
		return controller.ConnectionDetails{}, err
	}
	if externalURL != "" {
		props["externalEndpointURL"] = externalURL
		if strings.HasPrefix(externalURL, "https://") {
			props["externalVerifyTLS"] = strconv.FormatBool(externalHTTPSVerifyTLS(s3Params))
		}
	}

	return controller.ConnectionDetails{
		Type:                 "s3",
		Provider:             common.ProviderName,
		Host:                 host,
		Port:                 portStr,
		URI:                  endpoint,
		AdditionalProperties: props,
	}, nil
}

func externalHTTPSVerifyTLS(s3Params components.S3CustomSpec) bool {
	if s3Params.Ingress != nil && s3Params.Ingress.TLS != nil && s3Params.Ingress.TLS.VerifyTLS != nil {
		return *s3Params.Ingress.TLS.VerifyTLS
	}
	return true
}

// externalS3EndpointURL prefers an S3 Ingress URL when configured otherwise
// falls back to LoadBalancer / NodePort HTTP URLs.
func externalS3EndpointURL(c *controller.Context, svc *corev1.Service, port int32, sw *seaweedv1.Seaweed) (string, error) {
	if url, ok := s3IngressExternalURL(sw); ok {
		return url, nil
	}

	switch svc.Spec.Type {
	case corev1.ServiceTypeLoadBalancer:
		if ing := svc.Status.LoadBalancer.Ingress; len(ing) > 0 {
			extHost := ing[0].Hostname
			if extHost == "" {
				extHost = ing[0].IP
			}
			if extHost != "" {
				return s3HTTPURL(extHost, port), nil
			}
		}
	case corev1.ServiceTypeNodePort:
		nodePort := serviceS3NodePort(svc)
		if nodePort == 0 {
			return "", nil
		}
		hostIP, err := s3PodHostIP(c, svc)
		if err != nil || hostIP == "" {
			return "", err
		}
		return s3HTTPURL(hostIP, nodePort), nil
	}
	return "", nil
}

// s3IngressExternalURL returns the public S3 URL from Seaweed.spec.s3.ingress.
// https:// when TLS is configured. http:// otherwise. ok is false when Ingress is off.
func s3IngressExternalURL(sw *seaweedv1.Seaweed) (url string, ok bool) {
	if sw == nil || sw.Spec.S3 == nil || sw.Spec.S3.Ingress == nil || !sw.Spec.S3.Ingress.Enabled {
		return "", false
	}
	ing := sw.Spec.S3.Ingress
	if ing.Host == "" {
		return "", false
	}
	if len(ing.TLS) > 0 && ing.TLS[0].SecretName != "" {
		return "https://" + ing.Host, true
	}
	return "http://" + ing.Host, true
}

// s3PodHostIP returns the node IP of a ready S3 pod, as the PSMDB operator does
// for NodePort exposure.
func s3PodHostIP(c *controller.Context, svc *corev1.Service) (string, error) {
	if len(svc.Spec.Selector) == 0 {
		return "", nil
	}
	pods := &corev1.PodList{}
	if err := c.List(pods, client.MatchingLabels(svc.Spec.Selector)); err != nil {
		return "", fmt.Errorf("listing S3 pods: %w", err)
	}
	// Stable order keeps the published endpoint from flapping between reconciles.
	slices.SortFunc(pods.Items, func(a, b corev1.Pod) int { return strings.Compare(a.Name, b.Name) })
	for _, pod := range pods.Items {
		if pod.Status.HostIP == "" || pod.DeletionTimestamp != nil {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return pod.Status.HostIP, nil
			}
		}
	}
	return "", nil
}

func s3HTTPURL(host string, port int32) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func serviceS3NodePort(svc *corev1.Service) int32 {
	for _, p := range svc.Spec.Ports {
		if p.Name == s3HTTPPortName {
			return p.NodePort
		}
	}
	return 0
}

func serviceS3Port(svc *corev1.Service) int32 {
	for _, p := range svc.Spec.Ports {
		if p.Name == s3HTTPPortName || p.Port == defaultS3Port {
			return p.Port
		}
	}
	if len(svc.Spec.Ports) > 0 {
		return svc.Spec.Ports[0].Port
	}
	return defaultS3Port
}

func waitingForS3Ingress(c *controller.Context, sw *seaweedv1.Seaweed) (bool, error) {
	if sw.Spec.S3 == nil || sw.Spec.S3.Ingress == nil || !sw.Spec.S3.Ingress.Enabled {
		return false, nil
	}
	ing := &networkingv1.Ingress{}
	if err := c.Get(ing, s3IngressName(sw.Name)); err != nil {
		if controller.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}
