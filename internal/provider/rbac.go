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

// Run `make manifests` to regenerate config/rbac/role.yaml from these markers.
// This file contains kubebuilder RBAC markers for controller-gen.
// See: https://book.kubebuilder.io/reference/markers/rbac

// Base RBAC (required by all providers):
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.openeverest.io,resources=providers,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// SeaweedFS operator resources:
// +kubebuilder:rbac:groups=seaweed.seaweedfs.com,resources=seaweeds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=seaweed.seaweedfs.com,resources=seaweeds/status,verbs=get

// Scrape resources created when the topology exposes metrics:
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;create;patch;delete
// +kubebuilder:rbac:groups=operator.victoriametrics.com,resources=vmpodscrapes,verbs=get;create;patch;delete
// VMServiceScrapes the VictoriaMetrics Operator converts from seaweedfs-operator's
// ServiceMonitors, removed with the Instance:
// +kubebuilder:rbac:groups=operator.victoriametrics.com,resources=vmservicescrapes,verbs=deletecollection

// Operator-managed S3 Service (read for connection details):
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// Pods: S3 hostIP for NodePort connection details, and component pod counts
// in status.components (provider-runtime lists/watches labelled pods):
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Connection details Secret written by provider-runtime on Ready, and the
// S3 identities Secret (<name>-s3-config) the provider applies:
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Operator-managed S3 Ingress (read for HTTPS connection details):
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch

// PodDisruptionBudgets the provider creates per component:
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

// Storage classes named on master, volume, or filer. list resolves the default
// class when storage.size grows and no class is set:
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list

// Volume, filer, and master PVCs. Read during Status to report in-place expansion
// (operator image 1.0.40 patches these; it does not copy the new size onto Seaweed status):
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
