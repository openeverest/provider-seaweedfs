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
	"slices"
	"testing"

	"github.com/AlekSi/pointer"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	vmv1beta1 "github.com/openeverest/provider-seaweedfs/internal/apis/victoriametrics/v1beta1"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func exposeMetrics(prometheus, victoriaMetrics bool) *standalone.MetricsConfig {
	return &standalone.MetricsConfig{
		Prometheus:      &standalone.ScrapeConfig{Enabled: prometheus},
		VictoriaMetrics: &standalone.ScrapeConfig{Enabled: victoriaMetrics},
	}
}

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

// metricsContext builds a context for an Instance exposing metrics.
// withScrapeKinds controls whether the cluster serves PodMonitor and
// VMPodScrape, i.e. whether their operators' CRDs are installed. The
// provider's scheme knows the types either way.
func metricsContext(t *testing.T, metrics *standalone.MetricsConfig, withScrapeKinds bool, objs ...client.Object) (*controller.Context, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	require.NoError(t, vmv1beta1.AddToScheme(scheme))

	scrapeKinds := []schema.GroupVersionKind{
		monitoringv1.SchemeGroupVersion.WithKind(monitoringv1.PodMonitorsKind),
		vmv1beta1.GroupVersion.WithKind("VMPodScrape"),
		vmv1beta1.GroupVersion.WithKind("VMServiceScrape"),
	}
	mapper := meta.NewDefaultRESTMapper(nil)
	if withScrapeKinds {
		for _, gvk := range scrapeKinds {
			mapper.Add(gvk, meta.RESTScopeNamespace)
		}
	}
	// Without the CRDs the API server rejects any request for the kinds.
	notServed := func(obj client.Object) error {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil || withScrapeKinds || !slices.Contains(scrapeKinds, gvk) {
			return nil
		}
		return &meta.NoKindMatchError{GroupKind: gvk.GroupKind(), SearchedVersions: []string{gvk.Version}}
	}

	instance := newSyncInstance(validComponents())
	withTopology(t, instance, &standalone.StandaloneTopologyConfig{Metrics: metrics})

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRESTMapper(mapper).
		WithObjects(append(objs, instance)...).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if err := notServed(obj); err != nil {
					return err
				}
				return c.Delete(ctx, obj, opts...)
			},
			DeleteAllOf: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
				if err := notServed(obj); err != nil {
					return err
				}
				return c.DeleteAllOf(ctx, obj, opts...)
			},
		}).
		Build()
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName), cl
}

func metricsPorts(sw *seaweedv1.Seaweed) map[string]*int32 {
	ports := map[string]*int32{
		common.ComponentMaster: sw.Spec.Master.MetricsPort,
		common.ComponentVolume: sw.Spec.Volume.MetricsPort,
		common.ComponentFiler:  sw.Spec.Filer.MetricsPort,
		common.ComponentS3:     sw.Spec.S3.MetricsPort,
	}
	if sw.Spec.Admin != nil {
		ports[common.ComponentAdmin] = sw.Spec.Admin.MetricsPort
	}
	return ports
}

func TestApplyMetricsPorts(t *testing.T) {
	newSeaweed := func(admin bool) *seaweedv1.Seaweed {
		sw := &seaweedv1.Seaweed{Spec: seaweedv1.SeaweedSpec{
			Master: &seaweedv1.MasterSpec{},
			Volume: &seaweedv1.VolumeSpec{},
			Filer:  &seaweedv1.FilerSpec{},
			S3:     &seaweedv1.S3GatewaySpec{},
		}}
		if admin {
			sw.Spec.Admin = &seaweedv1.AdminSpec{}
		}
		return sw
	}

	t.Run("no metrics leaves every listener off", func(t *testing.T) {
		sw := newSeaweed(true)
		applyMetricsPorts(sw, nil)
		for component, port := range metricsPorts(sw) {
			assert.Nil(t, port, component)
		}
	})

	t.Run("metrics without scraping still turn every listener on", func(t *testing.T) {
		sw := newSeaweed(true)
		applyMetricsPorts(sw, &standalone.MetricsConfig{})
		ports := metricsPorts(sw)
		assert.Len(t, ports, 5)
		for component, port := range ports {
			require.NotNil(t, port, component)
			assert.Equal(t, MetricsPort, *port, component)
		}
	})

	t.Run("admin stays off when it is not deployed", func(t *testing.T) {
		sw := newSeaweed(false)
		applyMetricsPorts(sw, exposeMetrics(true, false))
		assert.Nil(t, sw.Spec.Admin)
		assert.Equal(t, MetricsPort, *sw.Spec.S3.MetricsPort)
	})
}

func TestSyncMetrics(t *testing.T) {
	getScrapes := func(t *testing.T, c *controller.Context) (pmErr, vmErr error) {
		t.Helper()
		return c.Get(&monitoringv1.PodMonitor{}, "test-instance-prometheus"),
			c.Get(&vmv1beta1.VMPodScrape{}, "test-instance-victoriametrics")
	}

	t.Run("exposes metrics on every component and creates a PodMonitor", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		withTopology(t, instance, &standalone.StandaloneTopologyConfig{
			Admin:   &standalone.AdminConfig{Enabled: true},
			Metrics: exposeMetrics(true, false),
		})
		c, sw := syncSeaweed(t, newSyncClient(t, instance), instance)

		ports := metricsPorts(sw)
		assert.Len(t, ports, 5)
		for component, port := range ports {
			require.NotNil(t, port, component)
			assert.Equal(t, MetricsPort, *port, component)
		}

		pm := &monitoringv1.PodMonitor{}
		require.NoError(t, c.Get(pm, "test-instance-prometheus"))
		require.Len(t, pm.OwnerReferences, 1, "the PodMonitor goes away with the Instance")
		assert.Equal(t, "Instance", pm.OwnerReferences[0].Kind)
		assert.Equal(t, map[string]string{controller.InstanceLabel: "test-instance"}, pm.Spec.Selector.MatchLabels)
		assert.Equal(t, []string{controller.InstanceLabel, controller.ComponentLabel}, pm.Spec.PodTargetLabels)
		var scrapedPorts []string
		for _, ep := range pm.Spec.PodMetricsEndpoints {
			require.NotNil(t, ep.Port)
			assert.Equal(t, "/metrics", ep.Path)
			scrapedPorts = append(scrapedPorts, *ep.Port)
		}
		assert.Equal(t, []string{"master-metrics", "volume-metrics", "filer-metrics", "s3-metrics", "admin-metrics"}, scrapedPorts)

		_, vmErr := getScrapes(t, c)
		assert.True(t, apierrors.IsNotFound(vmErr))
	})

	t.Run("switches scraping to VictoriaMetrics", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		withTopology(t, instance, &standalone.StandaloneTopologyConfig{Metrics: exposeMetrics(true, false)})
		// The VictoriaMetrics Operator converts the PodMonitor into a
		// VMPodScrape of the same name.
		converted := &vmv1beta1.VMPodScrape{ObjectMeta: metav1.ObjectMeta{Name: "test-instance-prometheus", Namespace: instance.Namespace}}
		cl := newSyncClient(t, instance, converted)
		syncSeaweed(t, cl, instance)

		withTopology(t, instance, &standalone.StandaloneTopologyConfig{Metrics: exposeMetrics(false, true)})
		c, sw := syncSeaweed(t, cl, instance)

		assert.Equal(t, MetricsPort, *sw.Spec.Master.MetricsPort)
		pmErr, vmErr := getScrapes(t, c)
		assert.True(t, apierrors.IsNotFound(pmErr))
		require.NoError(t, vmErr)

		scrape := &vmv1beta1.VMPodScrape{}
		require.NoError(t, c.Get(scrape, "test-instance-victoriametrics"))
		require.Len(t, scrape.OwnerReferences, 1)
		assert.Equal(t, map[string]string{controller.InstanceLabel: "test-instance"}, scrape.Spec.Selector.MatchLabels)
		assert.Equal(t, []string{controller.InstanceLabel, controller.ComponentLabel}, scrape.Spec.PodTargetLabels)
		assert.Len(t, scrape.Spec.PodMetricsEndpoints, len(metricsPortNames))

		err := c.Get(&vmv1beta1.VMPodScrape{}, converted.Name)
		assert.True(t, apierrors.IsNotFound(err), "the instance would stay scraped through the converted copy")
	})

	t.Run("exposes metrics without scraping", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		withTopology(t, instance, &standalone.StandaloneTopologyConfig{Metrics: &standalone.MetricsConfig{}})
		c, sw := syncSeaweed(t, newSyncClient(t, instance), instance)

		assert.Equal(t, MetricsPort, *sw.Spec.Filer.MetricsPort)
		pmErr, vmErr := getScrapes(t, c)
		assert.True(t, apierrors.IsNotFound(pmErr))
		assert.True(t, apierrors.IsNotFound(vmErr))
	})

	t.Run("stops exposing metrics", func(t *testing.T) {
		instance := newSyncInstance(validComponents())
		withTopology(t, instance, &standalone.StandaloneTopologyConfig{Metrics: exposeMetrics(true, true)})
		cl := newSyncClient(t, instance)
		c, _ := syncSeaweed(t, cl, instance)
		pmErr, vmErr := getScrapes(t, c)
		require.NoError(t, pmErr)
		require.NoError(t, vmErr)

		withTopology(t, instance, nil)
		c, sw := syncSeaweed(t, cl, instance)

		for component, port := range metricsPorts(sw) {
			assert.Nil(t, port, component)
		}
		pmErr, vmErr = getScrapes(t, c)
		assert.True(t, apierrors.IsNotFound(pmErr))
		assert.True(t, apierrors.IsNotFound(vmErr))
	})
}

func TestReconcileMetricsScrapes(t *testing.T) {
	t.Run("keeps the VMPodScrape converted from its PodMonitor while Prometheus scrapes", func(t *testing.T) {
		converted := &vmv1beta1.VMPodScrape{ObjectMeta: metav1.ObjectMeta{Name: "test-instance-prometheus", Namespace: "default"}}
		metrics := exposeMetrics(true, false)
		c, cl := metricsContext(t, metrics, true, converted)

		require.NoError(t, reconcileMetricsScrapes(c, metrics))

		require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(converted), &vmv1beta1.VMPodScrape{}))
	})

	t.Run("tolerates a cluster without the scrape kinds when disabled", func(t *testing.T) {
		c, _ := metricsContext(t, nil, false)
		require.NoError(t, reconcileMetricsScrapes(c, nil))

		metrics := &standalone.MetricsConfig{}
		c, _ = metricsContext(t, metrics, false)
		require.NoError(t, reconcileMetricsScrapes(c, metrics))
	})
}

func TestCleanupRemovesConvertedScrapes(t *testing.T) {
	serviceScrape := func(name, namespace, instance string) *vmv1beta1.VMServiceScrape {
		obj := &vmv1beta1.VMServiceScrape{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
		if instance != "" {
			obj.Labels = map[string]string{
				labelManagedBy: managedBySeaweedOperator,
				labelInstance:  instance,
			}
		}
		return obj
	}
	convertedPodMonitor := &vmv1beta1.VMPodScrape{ObjectMeta: metav1.ObjectMeta{Name: "test-instance-prometheus", Namespace: "default"}}
	// The converter copies the ServiceMonitors' labels.
	converted := []*vmv1beta1.VMServiceScrape{
		serviceScrape("test-instance-master", "default", "test-instance"),
		serviceScrape("test-instance-volume-0", "default", "test-instance"),
	}
	kept := []*vmv1beta1.VMServiceScrape{
		serviceScrape("other-master", "default", "other"),
		serviceScrape("test-instance-master", "elsewhere", "test-instance"),
		serviceScrape("test-instance-custom", "default", ""),
	}
	objs := []client.Object{convertedPodMonitor}
	for _, o := range append(converted, kept...) {
		objs = append(objs, o)
	}
	c, cl := metricsContext(t, exposeMetrics(true, false), true, objs...)

	require.NoError(t, New().Cleanup(c))

	err := cl.Get(context.Background(), client.ObjectKeyFromObject(convertedPodMonitor), &vmv1beta1.VMPodScrape{})
	assert.True(t, apierrors.IsNotFound(err))
	for _, o := range converted {
		err := cl.Get(context.Background(), client.ObjectKeyFromObject(o), &vmv1beta1.VMServiceScrape{})
		assert.True(t, apierrors.IsNotFound(err), o.Name)
	}
	for _, o := range kept {
		require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(o), &vmv1beta1.VMServiceScrape{}), o.Namespace+"/"+o.Name)
	}

	c, _ = metricsContext(t, nil, false)
	require.NoError(t, New().Cleanup(c), "nothing to remove without the VictoriaMetrics Operator")
}

func TestValidateMetrics(t *testing.T) {
	tests := []struct {
		name            string
		metrics         *standalone.MetricsConfig
		withScrapeKinds bool
		expectErr       string
	}{
		{name: "no metrics"},
		{name: "metrics without scraping need no operator", metrics: exposeMetrics(false, false)},
		{name: "operators installed", metrics: exposeMetrics(true, true), withScrapeKinds: true},
		{name: "Prometheus Operator missing", metrics: exposeMetrics(true, false), expectErr: "Prometheus Operator is not installed"},
		{name: "VictoriaMetrics Operator missing", metrics: exposeMetrics(false, true), expectErr: "VictoriaMetrics Operator is not installed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := metricsContext(t, tt.metrics, tt.withScrapeKinds)

			err := validateMetrics(c, tt.metrics)
			if tt.expectErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}

func TestValidateMetricsPort(t *testing.T) {
	assert.NoError(t, validateMetricsPort(nil, components.S3CustomSpec{Port: pointer.ToInt32(MetricsPort)}))
	assert.NoError(t, validateMetricsPort(exposeMetrics(true, false), components.S3CustomSpec{}))
	assert.NoError(t, validateMetricsPort(exposeMetrics(true, false), components.S3CustomSpec{Port: pointer.ToInt32(8333)}))

	err := validateMetricsPort(&standalone.MetricsConfig{}, components.S3CustomSpec{Port: pointer.ToInt32(MetricsPort)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics port")
}

func TestValidateRejectsMetricsWithoutOperator(t *testing.T) {
	c, _ := metricsContext(t, exposeMetrics(true, false), false)

	err := New().Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Prometheus Operator is not installed")
}
