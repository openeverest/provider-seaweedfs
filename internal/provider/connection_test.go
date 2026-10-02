package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func TestBuildConnectionDetailsFromService(t *testing.T) {
	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "sw", Namespace: "ns"},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	newCtx := func(objs ...client.Object) *controller.Context {
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithObjects(objs...).Build()
		return controller.NewContext(context.Background(), cl, instance, common.ProviderName)
	}
	ctx := newCtx()

	t.Run("ClusterIP", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
			},
		}

		details, err := buildConnectionDetailsFromService(ctx, svc)
		require.NoError(t, err)
		assert.Equal(t, "s3", details.Type)
		assert.Equal(t, common.ProviderName, details.Provider)
		assert.Equal(t, "sw-s3.ns.svc", details.Host)
		assert.Equal(t, "8333", details.Port)
		assert.Equal(t, "http://sw-s3.ns.svc:8333", details.URI)
		assert.Equal(t, "http://sw-s3.ns.svc:8333", details.AdditionalProperties["endpointURL"])
		assert.Equal(t, "true", details.AdditionalProperties["forcePathStyle"])
		assert.Equal(t, "false", details.AdditionalProperties["verifyTLS"])
		assert.Equal(t, defaultS3Region, details.AdditionalProperties["region"])
		assert.NotContains(t, details.AdditionalProperties, "externalEndpointURL")
	})

	t.Run("LoadBalancer with hostname ingress", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
				},
			},
		}

		details, err := buildConnectionDetailsFromService(ctx, svc)
		require.NoError(t, err)
		assert.Equal(t, "sw-s3.ns.svc", details.Host)
		assert.Equal(t, "http://sw-s3.ns.svc:8333", details.AdditionalProperties["endpointURL"])
		assert.Equal(t, "http://lb.example.com:8333", details.AdditionalProperties["externalEndpointURL"])
	})

	t.Run("LoadBalancer with IP ingress", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}},
				},
			},
		}

		details, err := buildConnectionDetailsFromService(ctx, svc)
		require.NoError(t, err)
		assert.Equal(t, "http://203.0.113.10:8333", details.AdditionalProperties["externalEndpointURL"])
	})

	t.Run("LoadBalancer with IPv6 ingress", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "2001:db8::10"}},
				},
			},
		}

		details, err := buildConnectionDetailsFromService(ctx, svc)
		require.NoError(t, err)
		assert.Equal(t, "http://[2001:db8::10]:8333", details.AdditionalProperties["externalEndpointURL"])
	})

	nodePortSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app.kubernetes.io/component": "s3", "app.kubernetes.io/instance": "sw"},
			Ports: []corev1.ServicePort{{
				Name:     "s3-http",
				Port:     8333,
				NodePort: 30080,
			}},
		},
	}
	s3Pod := func(name, hostIP string, ready bool) *corev1.Pod {
		readyStatus := corev1.ConditionFalse
		if ready {
			readyStatus = corev1.ConditionTrue
		}
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: nodePortSvc.Spec.Selector},
			Status: corev1.PodStatus{
				HostIP:     hostIP,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: readyStatus}},
			},
		}
	}

	t.Run("NodePort uses hostIP of first ready S3 pod", func(t *testing.T) {
		ctx := newCtx(
			s3Pod("sw-s3-a", "10.0.0.1", false),
			s3Pod("sw-s3-c", "10.0.0.3", true),
			s3Pod("sw-s3-b", "10.0.0.2", true),
		)

		details, err := buildConnectionDetailsFromService(ctx, nodePortSvc)
		require.NoError(t, err)
		assert.Equal(t, "sw-s3.ns.svc", details.Host)
		assert.Equal(t, "http://10.0.0.2:30080", details.AdditionalProperties["externalEndpointURL"])
	})

	t.Run("NodePort without ready S3 pod omits external endpoint", func(t *testing.T) {
		ctx := newCtx(s3Pod("sw-s3-a", "10.0.0.1", false))

		details, err := buildConnectionDetailsFromService(ctx, nodePortSvc)
		require.NoError(t, err)
		assert.NotContains(t, details.AdditionalProperties, "externalEndpointURL")
	})
}

func TestServiceS3Port(t *testing.T) {
	assert.Equal(t, int32(8333), serviceS3Port(&corev1.Service{
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}}},
	}))
	assert.Equal(t, int32(9000), serviceS3Port(&corev1.Service{
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "s3-http", Port: 9000}}},
	}))
	assert.Equal(t, int32(8333), serviceS3Port(&corev1.Service{}))
}
