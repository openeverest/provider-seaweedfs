package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).Build()
	ctx := controller.NewContext(context.Background(), cl, instance, common.ProviderName)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "sw-s3", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "s3-http", Port: 8333}},
		},
	}

	details := buildConnectionDetailsFromService(ctx, svc)
	assert.Equal(t, "s3", details.Type)
	assert.Equal(t, common.ProviderName, details.Provider)
	assert.Equal(t, "sw-s3.ns.svc", details.Host)
	assert.Equal(t, "8333", details.Port)
	assert.Equal(t, "http://sw-s3.ns.svc:8333", details.URI)
	assert.Equal(t, "true", details.AdditionalProperties["forcePathStyle"])
	assert.Equal(t, "false", details.AdditionalProperties["verifyTLS"])
	assert.Equal(t, defaultS3Region, details.AdditionalProperties["region"])
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
