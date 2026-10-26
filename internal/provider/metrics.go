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

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	vmv1beta1 "github.com/openeverest/provider-seaweedfs/internal/apis/victoriametrics/v1beta1"
)

const (
	// MetricsPort is the port every component serves metrics on. Each
	// component runs in its own pod, so they can share it. It stays clear of
	// the components' HTTP and gRPC ports and of the worker-lance sidecar's
	// 9328.
	MetricsPort int32 = 9327
	metricsPath       = "/metrics"
)

var (
	// metricsPortNames are the container ports seaweedfs-operator names after
	// each component's metricsPort. A pod exposes only its own component's,
	// so one scrape endpoint per name reaches every pod exactly once.
	metricsPortNames = []string{
		"master-metrics",
		"volume-metrics",
		"filer-metrics",
		"s3-metrics",
		"admin-metrics",
	}

	podMonitorGroupKind  = schema.GroupKind{Group: monitoringv1.SchemeGroupVersion.Group, Kind: monitoringv1.PodMonitorsKind}
	vmPodScrapeGroupKind = schema.GroupKind{Group: vmv1beta1.GroupVersion.Group, Kind: "VMPodScrape"}
)

func scrapeEnabled(scrape *standalone.ScrapeConfig) bool {
	return scrape != nil && scrape.Enabled
}

// applyMetricsPorts turns on the metrics listener of every component when the
// topology exposes metrics. seaweedfs-operator then also adds the port to the
// component's Services, and creates a ServiceMonitor for each component when
// the Prometheus Operator CRDs are installed.
func applyMetricsPorts(sw *seaweedv1.Seaweed, metrics *standalone.MetricsConfig) {
	if metrics == nil {
		return
	}
	sw.Spec.Master.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.Volume.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.Filer.MetricsPort = ptr.To(MetricsPort)
	sw.Spec.S3.MetricsPort = ptr.To(MetricsPort)
	if sw.Spec.Admin != nil {
		sw.Spec.Admin.MetricsPort = ptr.To(MetricsPort)
	}
}

// reconcileMetricsScrapes creates the scrape resource of every monitoring
// system the topology enables, and removes the others.
func reconcileMetricsScrapes(c *controller.Context, metrics *standalone.MetricsConfig) error {
	var prometheus, victoriaMetrics bool
	if metrics != nil {
		prometheus = scrapeEnabled(metrics.Prometheus)
		victoriaMetrics = scrapeEnabled(metrics.VictoriaMetrics)
	}

	if err := reconcileScrape(c, prometheus, podMonitor(c), &monitoringv1.PodMonitor{ObjectMeta: c.ObjectMeta(podMonitorName(c))}); err != nil {
		return fmt.Errorf("reconcile PodMonitor: %w", err)
	}
	if !prometheus {
		if err := deleteConvertedPodMonitor(c); err != nil {
			return err
		}
	}
	if err := reconcileScrape(c, victoriaMetrics, vmPodScrape(c), &vmv1beta1.VMPodScrape{ObjectMeta: c.ObjectMeta(vmPodScrapeName(c))}); err != nil {
		return fmt.Errorf("reconcile VMPodScrape: %w", err)
	}

	return nil
}

func reconcileScrape(c *controller.Context, enabled bool, desired, existing client.Object) error {
	if enabled {
		return c.Apply(desired)
	}
	return deleteIfServed(c, existing)
}

// deleteIfServed deletes obj; there is nothing to remove when the cluster
// does not serve its kind at all.
func deleteIfServed(c *controller.Context, obj client.Object) error {
	if err := c.Delete(obj); err != nil && !meta.IsNoMatchError(err) {
		return err
	}
	return nil
}

// deleteConvertedPodMonitor removes the VMPodScrape the VictoriaMetrics
// Operator converted from the instance's PodMonitor. The converter sets no
// owner reference by default and does not always remove it with the
// PodMonitor, and it would keep the instance scraped.
func deleteConvertedPodMonitor(c *controller.Context) error {
	converted := &vmv1beta1.VMPodScrape{ObjectMeta: c.ObjectMeta(podMonitorName(c))}
	if err := deleteIfServed(c, converted); err != nil {
		return fmt.Errorf("delete VMPodScrape converted from the PodMonitor: %w", err)
	}
	return nil
}

// deleteConvertedServiceMonitors removes the VMServiceScrapes the
// VictoriaMetrics Operator converted from seaweedfs-operator's ServiceMonitors.
// They have no owner reference by default and outlive the ServiceMonitors.
// The converter copies the source labels, and seaweedfs-operator creates no
// VMServiceScrapes itself, so its labels select only converted copies.
func deleteConvertedServiceMonitors(c *controller.Context) error {
	err := c.Client().DeleteAllOf(c.Context(), &vmv1beta1.VMServiceScrape{},
		client.InNamespace(c.Namespace()),
		client.MatchingLabels{
			labelManagedBy: managedBySeaweedOperator,
			labelInstance:  c.Name(),
		})
	if err != nil && !meta.IsNoMatchError(err) {
		return fmt.Errorf("delete VMServiceScrapes converted from the ServiceMonitors: %w", err)
	}
	return nil
}

// scrapeSelector selects the instance's pods; only those with a metrics
// listener expose the ports the scrape endpoints name.
func scrapeSelector(c *controller.Context) metav1.LabelSelector {
	return metav1.LabelSelector{MatchLabels: map[string]string{controller.InstanceLabel: c.Name()}}
}

// scrapeTargetLabels label every series with the instance and component.
func scrapeTargetLabels() []string {
	return []string{controller.InstanceLabel, controller.ComponentLabel}
}

// The scrape resources are named apart: the VictoriaMetrics Operator converts
// a PodMonitor into a VMPodScrape of the same name by default, and the
// provider must not mistake that one for its own. Neither name collides with
// the operator's <instance>-<component> ServiceMonitors.
func podMonitorName(c *controller.Context) string {
	return c.Name() + "-prometheus"
}

func vmPodScrapeName(c *controller.Context) string {
	return c.Name() + "-victoriametrics"
}

func podMonitor(c *controller.Context) *monitoringv1.PodMonitor {
	endpoints := make([]monitoringv1.PodMetricsEndpoint, 0, len(metricsPortNames))
	for _, port := range metricsPortNames {
		endpoints = append(endpoints, monitoringv1.PodMetricsEndpoint{Port: ptr.To(port), Path: metricsPath})
	}
	return &monitoringv1.PodMonitor{
		ObjectMeta: c.ObjectMeta(podMonitorName(c)),
		Spec: monitoringv1.PodMonitorSpec{
			Selector:            scrapeSelector(c),
			PodTargetLabels:     scrapeTargetLabels(),
			PodMetricsEndpoints: endpoints,
		},
	}
}

func vmPodScrape(c *controller.Context) *vmv1beta1.VMPodScrape {
	endpoints := make([]vmv1beta1.PodMetricsEndpoint, 0, len(metricsPortNames))
	for _, port := range metricsPortNames {
		endpoints = append(endpoints, vmv1beta1.PodMetricsEndpoint{Port: ptr.To(port), Path: metricsPath})
	}
	return &vmv1beta1.VMPodScrape{
		ObjectMeta: c.ObjectMeta(vmPodScrapeName(c)),
		Spec: vmv1beta1.VMPodScrapeSpec{
			Selector:            scrapeSelector(c),
			PodTargetLabels:     scrapeTargetLabels(),
			PodMetricsEndpoints: endpoints,
		},
	}
}

// validateMetricsPort rejects an S3 port that would take the metrics port in
// the S3 pod. The other components' ports are fixed.
func validateMetricsPort(metrics *standalone.MetricsConfig, s3 components.S3CustomSpec) error {
	if metrics == nil || s3.Port == nil || *s3.Port != MetricsPort {
		return nil
	}
	return fmt.Errorf("s3 port %d is the metrics port; pick another port or stop exposing metrics", MetricsPort)
}

// validateMetrics rejects scraping by a monitoring system whose operator is
// not installed, since its scrape resource could not be created.
func validateMetrics(c *controller.Context, metrics *standalone.MetricsConfig) error {
	if metrics == nil {
		return nil
	}

	checks := []struct {
		enabled  bool
		kind     schema.GroupKind
		version  string
		operator string
	}{
		{scrapeEnabled(metrics.Prometheus), podMonitorGroupKind, monitoringv1.Version, "Prometheus Operator"},
		{scrapeEnabled(metrics.VictoriaMetrics), vmPodScrapeGroupKind, vmv1beta1.GroupVersion.Version, "VictoriaMetrics Operator"},
	}
	for _, check := range checks {
		if !check.enabled {
			continue
		}
		if _, err := c.Client().RESTMapper().RESTMapping(check.kind, check.version); err != nil {
			if meta.IsNoMatchError(err) {
				return fmt.Errorf("metrics: %s is not installed: the cluster does not serve %s", check.operator, check.kind)
			}
			return fmt.Errorf("metrics: look up %s: %w", check.kind, err)
		}
	}

	return nil
}
