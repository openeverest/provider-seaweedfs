// Copyright (C) 2026 The OpenEverest Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// withTopology sets the instance's standalone topology parameters; a nil topo
// leaves the topology out.
func withTopology(t *testing.T, instance *corev1alpha1.Instance, topo *standalone.StandaloneTopologyConfig) {
	t.Helper()
	if topo == nil {
		instance.Spec.Topology = nil
		return
	}
	raw, err := json.Marshal(topo)
	require.NoError(t, err)
	instance.Spec.Topology = &corev1alpha1.TopologySpec{
		Type:       "standalone",
		Parameters: &runtime.RawExtension{Raw: raw},
	}
}

// monitoringContext builds a context for an Instance on a cluster that serves
// the legacy scrape kinds when withScrapeKinds is set, i.e. when their
// operators' CRDs are installed.
func monitoringContext(t *testing.T, withScrapeKinds bool, objs ...client.Object) (*controller.Context, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, seaweedv1.AddToScheme(scheme))
	// The fake client needs the list kind to delete a collection; a cluster
	// needs only the CRD.
	scheme.AddKnownTypeWithName(vmServiceScrapeGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(vmServiceScrapeGVK.GroupVersion().WithKind("VMServiceScrapeList"), &unstructured.UnstructuredList{})

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(seaweedv1.GroupVersion.WithKind("Seaweed"), meta.RESTScopeNamespace)
	if withScrapeKinds {
		for _, gvk := range []schema.GroupVersionKind{podMonitorGVK, vmPodScrapeGVK, vmServiceScrapeGVK} {
			mapper.Add(gvk, meta.RESTScopeNamespace)
		}
	}

	instance := newSyncInstance(validComponents())
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRESTMapper(mapper).
		WithObjects(append(objs, instance)...).
		Build()
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName), cl
}

func TestApplyMetricsPorts(t *testing.T) {
	sw := &seaweedv1.Seaweed{Spec: seaweedv1.SeaweedSpec{
		Master: &seaweedv1.MasterSpec{},
		Volume: &seaweedv1.VolumeSpec{},
		Filer:  &seaweedv1.FilerSpec{},
		S3:     &seaweedv1.S3GatewaySpec{},
		Admin:  &seaweedv1.AdminSpec{},
	}}

	applyMetricsPorts(sw)

	for name, port := range map[string]*int32{
		common.ComponentMaster: sw.Spec.Master.MetricsPort,
		common.ComponentVolume: sw.Spec.Volume.MetricsPort,
		common.ComponentFiler:  sw.Spec.Filer.MetricsPort,
		common.ComponentS3:     sw.Spec.S3.MetricsPort,
	} {
		require.NotNil(t, port, name)
		assert.Equal(t, MetricsPort, *port, name)
	}
	assert.Nil(t, sw.Spec.Admin.MetricsPort, "the admin server is not an Instance component")
	assert.False(t, sw.Spec.ServiceMonitorsEnabled(), "the operator's ServiceMonitors would scrape every pod twice")
}

func TestMonitoringSources(t *testing.T) {
	seaweed := func(withPorts bool, readyMaster, readyS3 int32) *seaweedv1.Seaweed {
		sw := &seaweedv1.Seaweed{
			ObjectMeta: metav1.ObjectMeta{Name: "test-instance", Namespace: "default"},
			Spec: seaweedv1.SeaweedSpec{
				Master: &seaweedv1.MasterSpec{},
				Volume: &seaweedv1.VolumeSpec{},
				Filer:  &seaweedv1.FilerSpec{},
				S3:     &seaweedv1.S3GatewaySpec{},
			},
		}
		if withPorts {
			applyMetricsPorts(sw)
		}
		sw.Status.Master.ReadyReplicas = readyMaster
		sw.Status.S3.ReadyReplicas = readyS3
		return sw
	}
	endpoint := func(component string) corev1alpha1.MetricsEndpoint {
		return corev1alpha1.MetricsEndpoint{
			Component: component,
			PodSelector: map[string]string{
				controller.InstanceLabel:  "test-instance",
				controller.ComponentLabel: component,
			},
			Port:   intstr.FromString(component + "-metrics"),
			Path:   "/metrics",
			Scheme: "http",
		}
	}

	tests := []struct {
		name string
		objs []client.Object
		want []corev1alpha1.MetricsEndpoint
	}{
		{name: "no Seaweed cluster"},
		{name: "no pod ready", objs: []client.Object{seaweed(true, 0, 0)}},
		{name: "metrics port not applied yet", objs: []client.Object{seaweed(false, 3, 1)}},
		{
			name: "ready components",
			objs: []client.Object{seaweed(true, 1, 1)},
			want: []corev1alpha1.MetricsEndpoint{endpoint(common.ComponentMaster), endpoint(common.ComponentS3)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := monitoringContext(t, false, tt.objs...)
			sources, err := New().MonitoringSources(c)
			require.NoError(t, err)
			require.NotNil(t, sources, "nil would clear the published sources")
			assert.Equal(t, tt.want, sources.Metrics)
		})
	}
}

func TestValidateMetrics(t *testing.T) {
	instance := func(t *testing.T, topo *standalone.StandaloneTopologyConfig, s3Port *int32) *corev1alpha1.Instance {
		in := newSyncInstance(validComponents())
		withTopology(t, in, topo)
		if s3Port != nil {
			s3 := in.Spec.Components[common.ComponentS3]
			raw, err := json.Marshal(components.S3CustomSpec{Port: s3Port})
			require.NoError(t, err)
			s3.Parameters = &runtime.RawExtension{Raw: raw}
			in.Spec.Components[common.ComponentS3] = s3
		}
		return in
	}

	t.Run("deprecated metrics parameter is accepted with a warning", func(t *testing.T) {
		//nolint:staticcheck // Writing the deprecated parameter.
		res := New().Validate(controller.ValidationInput{Instance: instance(t, &standalone.StandaloneTopologyConfig{
			Metrics: &standalone.MetricsConfig{Prometheus: &standalone.ScrapeConfig{Enabled: true}},
		}, nil)})
		assert.Empty(t, res.Errors)
		require.Len(t, res.Warnings, 1)
		assert.Contains(t, res.Warnings[0], "spec.topology.parameters.metrics: deprecated and ignored")
	})

	t.Run("no warning without the parameter", func(t *testing.T) {
		res := New().Validate(controller.ValidationInput{Instance: instance(t, &standalone.StandaloneTopologyConfig{}, nil)})
		assert.Empty(t, res.Errors)
		assert.Empty(t, res.Warnings)
	})

	t.Run("the S3 port cannot be the metrics port", func(t *testing.T) {
		res := New().Validate(controller.ValidationInput{Instance: instance(t, nil, pointer.ToInt32(MetricsPort))})
		require.Len(t, res.Errors, 1)
		assert.Equal(t, "spec.components[s3].parameters.port", res.Errors[0].Field)
	})
}

func TestDeleteLegacyScrapes(t *testing.T) {
	legacy := func(gvk schema.GroupVersionKind, name, namespace string, labels map[string]string) *unstructured.Unstructured {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(name)
		obj.SetNamespace(namespace)
		obj.SetLabels(labels)
		return obj
	}
	converted := map[string]string{labelManagedBy: managedBySeaweedOperator, labelInstance: "test-instance"}

	named := []*unstructured.Unstructured{
		legacy(podMonitorGVK, "test-instance-prometheus", "default", nil),
		legacy(vmPodScrapeGVK, "test-instance-victoriametrics", "default", nil),
		legacy(vmPodScrapeGVK, "test-instance-prometheus", "default", nil),
	}
	convertedScrapes := []*unstructured.Unstructured{
		legacy(vmServiceScrapeGVK, "test-instance-master", "default", converted),
		legacy(vmServiceScrapeGVK, "test-instance-volume", "default", converted),
	}
	kept := []*unstructured.Unstructured{
		legacy(podMonitorGVK, "other-prometheus", "default", nil),
		legacy(vmServiceScrapeGVK, "other-master", "default",
			map[string]string{labelManagedBy: managedBySeaweedOperator, labelInstance: "other"}),
		legacy(vmServiceScrapeGVK, "test-instance-master", "elsewhere", converted),
		legacy(vmServiceScrapeGVK, "test-instance-custom", "default", nil),
	}
	var objs []client.Object
	for _, o := range append(append(named, convertedScrapes...), kept...) {
		objs = append(objs, o)
	}

	for _, run := range []struct {
		name    string
		run     func(*controller.Context) error
		deleted []*unstructured.Unstructured
		kept    []*unstructured.Unstructured
	}{
		{"cleanup", New().Cleanup, append(append([]*unstructured.Unstructured{}, named...), convertedScrapes...), kept},
		{"sync", func(c *controller.Context) error {
			if err := deleteLegacyScrapes(c); err != nil {
				return err
			}
			return deleteConvertedServiceMonitors(c)
		}, append(append([]*unstructured.Unstructured{}, named...), convertedScrapes...), kept},
	} {
		t.Run(run.name, func(t *testing.T) {
			var copies []client.Object
			for _, o := range objs {
				copies = append(copies, o.DeepCopyObject().(client.Object))
			}
			c, cl := monitoringContext(t, true, copies...)

			require.NoError(t, run.run(c))

			for _, o := range run.deleted {
				got := &unstructured.Unstructured{}
				got.SetGroupVersionKind(o.GroupVersionKind())
				err := cl.Get(context.Background(), client.ObjectKeyFromObject(o), got)
				assert.True(t, apierrors.IsNotFound(err), "%s %s should be deleted", o.GetKind(), o.GetName())
			}
			for _, o := range run.kept {
				got := &unstructured.Unstructured{}
				got.SetGroupVersionKind(o.GroupVersionKind())
				assert.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(o), got),
					"%s %s/%s should be kept", o.GetKind(), o.GetNamespace(), o.GetName())
			}
		})
	}

	t.Run("cluster without the scrape kinds", func(t *testing.T) {
		c, _ := monitoringContext(t, false)
		require.NoError(t, New().Cleanup(c))
	})
}
