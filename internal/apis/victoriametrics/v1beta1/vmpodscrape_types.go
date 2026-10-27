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

// Package v1beta1 holds the subset of the VictoriaMetrics Operator API
// (operator.victoriametrics.com/v1beta1) the provider writes or deletes. The
// upstream API module depends on all of VictoriaMetrics; these types carry
// only the fields the provider sets and serialize to the same JSON.
//
// +kubebuilder:object:generate=true
// +groupName=operator.victoriametrics.com
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the VictoriaMetrics Operator API group and version.
	GroupVersion = schema.GroupVersion{Group: "operator.victoriametrics.com", Version: "v1beta1"}

	// SchemeBuilder registers the types with a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&VMPodScrape{}, &VMPodScrapeList{},
		&VMServiceScrape{}, &VMServiceScrapeList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// VMPodScrape tells the VictoriaMetrics Operator to scrape the pods the
// selector matches.
//
// +kubebuilder:object:root=true
type VMPodScrape struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec VMPodScrapeSpec `json:"spec"`
}

// VMPodScrapeSpec selects the pods to scrape and the endpoints to scrape on
// them.
type VMPodScrapeSpec struct {
	// PodTargetLabels are pod labels copied onto every scraped series.
	PodTargetLabels []string `json:"podTargetLabels,omitempty"`

	// PodMetricsEndpoints are the endpoints to scrape on each selected pod.
	PodMetricsEndpoints []PodMetricsEndpoint `json:"podMetricsEndpoints"`

	// Selector selects the pods to scrape in the VMPodScrape's namespace.
	Selector metav1.LabelSelector `json:"selector,omitempty"`
}

// PodMetricsEndpoint is one endpoint scraped on a pod.
type PodMetricsEndpoint struct {
	// Port is the name of the pod port that serves metrics.
	Port *string `json:"port,omitempty"`

	// Path is the HTTP path metrics are served on.
	Path string `json:"path,omitempty"`
}

// VMPodScrapeList is a list of VMPodScrapes.
//
// +kubebuilder:object:root=true
type VMPodScrapeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []VMPodScrape `json:"items"`
}

// VMServiceScrape tells the VictoriaMetrics Operator to scrape the endpoints
// of the Services the selector matches. The provider only deletes them, so
// the spec is left out.
//
// +kubebuilder:object:root=true
type VMServiceScrape struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

// VMServiceScrapeList is a list of VMServiceScrapes.
//
// +kubebuilder:object:root=true
type VMServiceScrapeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []VMServiceScrape `json:"items"`
}
