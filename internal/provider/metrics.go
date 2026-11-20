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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

const (
	// MetricsPort is the port every component serves metrics on. Each
	// component runs in its own pod, so they can share it. It stays clear of
	// the components' HTTP and gRPC ports and of the worker-lance sidecar's
	// 9328.
	MetricsPort int32 = 9327
	metricsPath       = "/metrics"
)

var _ controller.MonitoringSourcesProvider = (*Provider)(nil)

// metricsComponents are the Instance components that serve metrics. The admin
// server serves none: it is not an Instance component, so the runtime would
// not publish its endpoint.
var metricsComponents = []string{
	common.ComponentMaster,
	common.ComponentVolume,
	common.ComponentFiler,
	common.ComponentS3,
}

// metricsPortName is the container port seaweedfs-operator names after a
// component's metricsPort.
func metricsPortName(component string) string {
	return component + "-metrics"
}

// applyMetricsPorts serves metrics on every component (metrics: Native in
// definition/monitoring.yaml), so binding a monitoring destination restarts
// nothing. seaweedfs-operator's own ServiceMonitors are turned off: a
// monitoring extension scrapes the published endpoints, and both would scrape
// every pod twice. The operator removes the ones it created earlier.
func applyMetricsPorts(sw *seaweedv1.Seaweed) {
	sw.Spec.Master.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.Volume.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.Filer.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.S3.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.ServiceMonitor = &seaweedv1.ServiceMonitorSpec{Enabled: ptr.To(false)}
}

// MonitoringSources implements controller.MonitoringSourcesProvider. A
// component's endpoint is published once its metrics port is on the Seaweed
// spec and one of its pods is ready, and stays published while later changes
// roll the pods. Monitoring extensions turn the endpoints into scrape
// objects; other scrapers can read them from the Instance status.
func (p *Provider) MonitoringSources(c *controller.Context) (*corev1alpha1.MonitoringSources, error) {
	sw := &seaweedv1.Seaweed{}
	found, err := c.Exists(sw, c.Name())
	if err != nil {
		return nil, fmt.Errorf("get Seaweed cluster: %w", err)
	}
	sources := &corev1alpha1.MonitoringSources{}
	if !found {
		return sources, nil
	}
	for _, component := range metricsComponents {
		if !servesMetrics(sw, component) {
			continue
		}
		sources.Metrics = append(sources.Metrics, corev1alpha1.MetricsEndpoint{
			Component: component,
			PodSelector: map[string]string{
				controller.InstanceLabel:  c.Name(),
				controller.ComponentLabel: component,
			},
			Port:   intstr.FromString(metricsPortName(component)),
			Path:   metricsPath,
			Scheme: "http",
		})
	}
	return sources, nil
}

// servesMetrics reports whether the component's metrics port is on the
// Seaweed spec and one of its pods is ready.
func servesMetrics(sw *seaweedv1.Seaweed, component string) bool {
	var port *int32
	var ready int32
	switch component {
	case common.ComponentMaster:
		if sw.Spec.Master != nil {
			port = sw.Spec.Master.MetricsPort
		}
		ready = sw.Status.Master.ReadyReplicas
	case common.ComponentVolume:
		if sw.Spec.Volume != nil {
			port = sw.Spec.Volume.MetricsPort
		}
		ready = sw.Status.Volume.ReadyReplicas
	case common.ComponentFiler:
		if sw.Spec.Filer != nil {
			port = sw.Spec.Filer.MetricsPort
		}
		ready = sw.Status.Filer.ReadyReplicas
	case common.ComponentS3:
		if sw.Spec.S3 != nil {
			port = sw.Spec.S3.MetricsPort
		}
		ready = sw.Status.S3.ReadyReplicas
	}
	return port != nil && ready > 0
}

// validateMetricsPort rejects an S3 port that would take the metrics port in
// the S3 pod. The other components' ports are fixed.
func validateMetricsPort(s3 components.S3CustomSpec) error {
	if s3.Port == nil || *s3.Port != MetricsPort {
		return nil
	}
	return fmt.Errorf("s3 port %d is the metrics port; pick another port", MetricsPort)
}

// metricsWarnings reports the deprecated metrics parameter: it has no effect,
// and metrics are collected through spec.monitoring.destinations.
func metricsWarnings(instance *corev1alpha1.Instance) []string {
	if instance.Spec.Topology == nil {
		return nil
	}
	var topo standalone.StandaloneTopologyConfig
	//nolint:staticcheck // Reporting the deprecated parameter.
	if !decodeParameters(instance.Spec.Topology.Parameters, &topo) || topo.Metrics == nil {
		return nil
	}
	return []string{topologyParametersPath.Child("metrics").String() +
		": deprecated and ignored; every component serves metrics and the provider no longer creates a PodMonitor or VMPodScrape. " +
		"List a MonitoringDestination in spec.monitoring.destinations to collect metrics"}
}

// Scrape objects releases before the monitoring contract created, deleted as
// metadata only so the provider needs neither their types nor their CRDs.
var (
	podMonitorGVK      = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}
	vmPodScrapeGVK     = schema.GroupVersionKind{Group: "operator.victoriametrics.com", Version: "v1beta1", Kind: "VMPodScrape"}
	vmServiceScrapeGVK = schema.GroupVersionKind{Group: "operator.victoriametrics.com", Version: "v1beta1", Kind: "VMServiceScrape"}
)

// deleteLegacyScrapes removes the scrape objects of releases before the
// monitoring contract: the PodMonitor and VMPodScrape created for
// spec.topology.parameters.metrics, and the VMPodScrape the VictoriaMetrics
// Operator converts from that PodMonitor, which has no owner reference.
// Clusters without their CRDs are skipped.
// TODO: drop once no supported release created them.
func deleteLegacyScrapes(c *controller.Context) error {
	legacy := []struct {
		gvk  schema.GroupVersionKind
		name string
	}{
		{podMonitorGVK, c.Name() + "-prometheus"},
		{vmPodScrapeGVK, c.Name() + "-victoriametrics"},
		{vmPodScrapeGVK, c.Name() + "-prometheus"},
	}
	for _, l := range legacy {
		obj := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: l.name, Namespace: c.Namespace()}}
		obj.SetGroupVersionKind(l.gvk)
		if err := c.Delete(obj); err != nil && !meta.IsNoMatchError(err) {
			return fmt.Errorf("delete legacy %s %s: %w", l.gvk.Kind, l.name, err)
		}
	}
	return nil
}

// deleteConvertedServiceMonitors removes the VMServiceScrapes the
// VictoriaMetrics Operator converted from the ServiceMonitors
// seaweedfs-operator created before the provider turned them off. They have
// no owner reference and carry the ServiceMonitors' labels. It runs on every
// Sync, so a copy converted again before the operator removed its source is
// deleted too.
func deleteConvertedServiceMonitors(c *controller.Context) error {
	scrapes := &metav1.PartialObjectMetadata{}
	scrapes.SetGroupVersionKind(vmServiceScrapeGVK)
	err := c.Client().DeleteAllOf(c.Context(), scrapes,
		client.InNamespace(c.Namespace()),
		client.MatchingLabels{
			labelManagedBy: managedBySeaweedOperator,
			labelInstance:  c.Name(),
		})
	// A scheme without the kind, as in clients that know only the provider's
	// types, means the same as a cluster without the CRD.
	if err != nil && !meta.IsNoMatchError(err) && !runtime.IsNotRegisteredError(err) {
		return fmt.Errorf("delete VMServiceScrapes converted from the ServiceMonitors: %w", err)
	}
	return nil
}
