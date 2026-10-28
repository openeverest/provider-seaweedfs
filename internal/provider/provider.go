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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
	vmv1beta1 "github.com/openeverest/provider-seaweedfs/internal/apis/victoriametrics/v1beta1"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// Compile-time check that Provider implements the required interface.
var _ controller.ProviderInterface = (*Provider)(nil)

// Provider implements controller.ProviderInterface for the provider-seaweedfs provider.
type Provider struct {
	controller.BaseProvider
}

// New creates a new Provider instance.
func New() *Provider {
	return &Provider{
		BaseProvider: controller.BaseProvider{
			ProviderName: common.ProviderName,
			SchemeFuncs: []func(*runtime.Scheme) error{
				seaweedv1.AddToScheme,
				networkingv1.AddToScheme,
				policyv1.AddToScheme,
				// Scrape resources for the exposed metrics.
				monitoringv1.AddToScheme,
				vmv1beta1.AddToScheme,
			},
			WatchConfigs: []controller.WatchConfig{
				controller.WatchOwned(&seaweedv1.Seaweed{}),
				controller.WatchOwned(&seaweedv1.SeaweedBackup{}),
				// Re-apply the S3 identities Secret if it is edited or deleted.
				controller.WatchOwned(&corev1.Secret{},
					predicate.NewPredicateFuncs(func(o client.Object) bool {
						return strings.HasSuffix(o.GetName(), s3ConfigSecretSuffix)
					})),
				controller.WatchOwned(&policyv1.PodDisruptionBudget{}),
				// The operator does not touch the Seaweed CR when LB ingress or NodePort
				// changes, so watch the S3 Service directly to refresh status.
				controller.WatchExternal(&corev1.Service{},
					handler.EnqueueRequestsFromMapFunc(s3ServiceToInstance)),
				// S3 Ingress is owned by Seaweed so watch it for HTTPS connection details.
				controller.WatchExternal(&networkingv1.Ingress{},
					handler.EnqueueRequestsFromMapFunc(s3IngressToInstance)),
				// cert-manager owns the server TLS Secret so watch by name so Status
				// can leave Provisioning once mTLS material exists.
				// Note: the informer still caches all Secrets cluster-wide; the
				// predicate only drops unrelated events before enqueue.
				controller.WatchExternal(&corev1.Secret{},
					handler.EnqueueRequestsFromMapFunc(tlsSecretToInstance),
					predicate.NewPredicateFuncs(func(o client.Object) bool {
						return strings.HasSuffix(o.GetName(), tlsServerSecretSuffix)
					})),
				// PVC resize conditions change without a Seaweed status update.
				// The operator label drops unrelated claims; name substrings
				// such as "-volume-" match PVCs this provider does not own.
				controller.WatchExternal(&corev1.PersistentVolumeClaim{},
					handler.EnqueueRequestsFromMapFunc(pvcToInstance),
					predicate.NewPredicateFuncs(func(o client.Object) bool {
						return o.GetLabels()[labelManagedBy] == managedBySeaweedOperator
					})),
			},
		},
	}
}

// s3ServiceToInstance maps the operator-owned S3 Service to its Instance; the
// Seaweed CR shares the Instance name.
func s3ServiceToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	owner := metav1.GetControllerOf(obj)
	if owner == nil || owner.Kind != "Seaweed" || owner.APIVersion != seaweedv1.GroupVersion.String() {
		return nil
	}
	if obj.GetName() != s3ServiceName(owner.Name) {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: owner.Name},
	}}
}

// Selector labels seaweedfs-operator sets on volume, filer, and master
// StatefulSets. Kubernetes copies spec.selector.matchLabels onto PVCs
// created from volumeClaimTemplates.
const (
	labelManagedBy           = "app.kubernetes.io/managed-by"
	labelComponent           = "app.kubernetes.io/component"
	labelInstance            = "app.kubernetes.io/instance"
	managedBySeaweedOperator = "seaweedfs-operator"
)

// pvcToInstance maps a volume, filer, or master PVC back to the Instance.
// The operator sets persistentVolumeClaimRetentionPolicy.whenDeleted to
// Retain, so Kubernetes adds no StatefulSet controller owner reference.
// With enablePVReclaim, a controller reference is added only after scale-down,
// and it points at the Pod. Selector labels are present on the live claim.
func pvcToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels[labelManagedBy] != managedBySeaweedOperator {
		return nil
	}
	switch labels[labelComponent] {
	case common.ComponentVolume, common.ComponentFiler, common.ComponentMaster:
	default:
		return nil
	}
	instance := labels[labelInstance]
	if instance == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: instance},
	}}
}

// s3IngressToInstance maps the operator-owned S3 Ingress to its Instance.
func s3IngressToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	owner := metav1.GetControllerOf(obj)
	if owner == nil || owner.Kind != "Seaweed" || owner.APIVersion != seaweedv1.GroupVersion.String() {
		return nil
	}
	if obj.GetName() != s3IngressName(owner.Name) {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: owner.Name},
	}}
}

// Sync ensures all required resources exist and are configured correctly.
//
// This is the main reconciliation logic. Create or update your operator
// operator's custom resource(s) based on the Instance spec.
func (p *Provider) Sync(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Syncing instance", "name", c.Name())

	master := c.Instance().Spec.Components[common.ComponentMaster]
	volume := c.Instance().Spec.Components[common.ComponentVolume]
	filer := c.Instance().Spec.Components[common.ComponentFiler]
	s3 := c.Instance().Spec.Components[common.ComponentS3]

	var masterCustomSpec components.MasterCustomSpec
	c.TryDecodeComponentParameters(master, &masterCustomSpec)

	var volumeCustomSpec components.VolumeCustomSpec
	c.TryDecodeComponentParameters(volume, &volumeCustomSpec)

	var filerCustomSpec components.FilerCustomSpec
	c.TryDecodeComponentParameters(filer, &filerCustomSpec)

	var s3CustomSpec components.S3CustomSpec
	c.TryDecodeComponentParameters(s3, &s3CustomSpec)

	var topo standalone.StandaloneTopologyConfig
	c.TryDecodeTopologyParameters(&topo)

	image, err := resolveImage(c, common.ComponentMaster, master)
	if err != nil {
		return err
	}

	s3ConfigChecksum, err := syncS3ConfigSecret(c)
	if err != nil {
		return err
	}

	s3TLSSecret, err := ensureS3ServerTLS(c, s3CustomSpec)
	if err != nil {
		return err
	}

	adminSecretName, err := ensureAdminCredentials(c, topo)
	if err != nil {
		return err
	}

	backupSpec, err := buildBackupSpec(c)
	if err != nil {
		return err
	}

	sw := &seaweedv1.Seaweed{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: seaweedv1.SeaweedSpec{
			Image:                 image,
			VolumeServerDiskCount: pointer.ToInt32(DefaultVolumeServerDiskCount),
			Master:                buildMasterSpec(master, masterCustomSpec, effectiveReplication(topo.Replication, *volume.Replicas)),
			Volume:                buildVolumeSpec(volume, volumeCustomSpec),
			Filer:                 buildFilerSpec(filer, filerCustomSpec),
			S3:                    buildS3Spec(s3, s3CustomSpec, s3TLSSecret),
			Admin:                 buildAdminSpec(adminSecretName),
			TLS:                   buildTLSSpec(topo),
			SecurityConfig:        buildSecurityConfigSpec(topo),
			Backup:                backupSpec,
		},
	}

	if topo.VolumeServerDiskCount != nil {
		sw.Spec.VolumeServerDiskCount = topo.VolumeServerDiskCount
	}
	applyS3Auth(sw.Spec.S3, c.Name(), s3ConfigChecksum)
	applyMetricsPorts(sw, topo.Metrics)
	labelPods(c, sw)
	applyScheduling(c, sw)

	if err := c.Apply(sw); err != nil {
		return err
	}
	if err := syncPodDisruptionBudgets(c); err != nil {
		return err
	}
	return reconcileMetricsScrapes(c, topo.Metrics)
}

// labelPods labels every component's pods so the runtime counts them into
// the Instance's status.components. The operator adds them to the pod
// templates only, never to the StatefulSet/Deployment selectors.
// Admin is labelled only when the user enabled it. It is not an Instance
// component; the operator runs that one pod from spec.admin.
func labelPods(c *controller.Context, sw *seaweedv1.Seaweed) {
	sw.Spec.Master.Labels = c.PodLabels(common.ComponentMaster)
	sw.Spec.Volume.Labels = c.PodLabels(common.ComponentVolume)
	sw.Spec.Filer.Labels = c.PodLabels(common.ComponentFiler)
	sw.Spec.S3.Labels = c.PodLabels(common.ComponentS3)
	if sw.Spec.Admin != nil {
		sw.Spec.Admin.Labels = c.PodLabels(common.ComponentAdmin)
	}
}

func resolveImage(c *controller.Context, componentName string, comp corev1alpha1.ComponentSpec) (string, error) {
	if comp.Image != "" {
		return comp.Image, nil
	}

	spec, err := c.ProviderSpec()
	if err != nil {
		return "", fmt.Errorf("resolving image for %q: %w", componentName, err)
	}

	if comp.Version != "" {
		if image := controller.GetImageForVersion(spec, componentName, comp.Version); image != "" {
			return image, nil
		}
	}

	if image := controller.GetDefaultImageForComponent(spec, componentName); image != "" {
		return image, nil
	}

	return "", fmt.Errorf("no image found for component %q", componentName)
}

func getS3Service(c *controller.Context, svc *corev1.Service) error {
	if err := c.Get(svc, s3ServiceName(c.Name())); err != nil {
		return err
	}
	return nil
}

// Status computes the current status of the database instance.
//
// Query the operator's resource(s) and translate their status
// into the provider-runtime's Status type.
func (p *Provider) Status(c *controller.Context) (controller.Status, error) {
	l := log.FromContext(c.Context())
	l.Info("Computing status", "name", c.Name())

	sw := &seaweedv1.Seaweed{}
	if err := c.Get(sw, c.Name()); err != nil {
		if controller.IsNotFound(err) {
			return controller.Pending("Waiting to get SeaweedFS cluster resource"), nil
		}
		return controller.Status{}, err
	}

	var s3Params components.S3CustomSpec
	if s3Comp, ok := c.Instance().Spec.Components[common.ComponentS3]; ok {
		c.TryDecodeComponentParameters(s3Comp, &s3Params)
	}
	// Surface a missing user-supplied S3 HTTPS Secret before a generic pod
	// not-ready message from a failed volume mount.
	waitingS3TLS, err := waitingForS3HTTPSSecret(c, s3Params)
	if err != nil {
		return controller.Status{}, err
	}
	if waitingS3TLS {
		return controller.Provisioning(fmt.Sprintf(
			"Waiting for S3 HTTPS TLS Secret %q (tls.crt and tls.key)", s3Params.HTTPS.SecretName)), nil
	}

	if cond := meta.FindStatusCondition(sw.Status.Conditions, "Ready"); cond != nil {
		if cond.Status == metav1.ConditionTrue {
			// Inter-component mTLS: the operator has no TLSReady condition and
			// silently no-ops when cert-manager is missing. Gate Ready on the
			// server TLS Secret so a missing cert-manager surfaces clearly.
			waiting, err := waitingForTLSSecret(c, sw)
			if err != nil {
				return controller.Status{}, err
			}
			if waiting {
				return controller.Provisioning(
					"Waiting for TLS certificate Secret (requires cert-manager; restart seaweedfs-operator if cert-manager was installed after it)"), nil
			}

			svc := &corev1.Service{}
			if err := getS3Service(c, svc); err != nil {
				if controller.IsNotFound(err) {
					return controller.Provisioning("Waiting for S3 Service"), nil
				}
				return controller.Status{}, err
			}
			// Stay Provisioning until LoadBalancer ingress is assigned so
			// connection details can include an actionable externalEndpointURL.
			if svc.Spec.Type == corev1.ServiceTypeLoadBalancer && len(svc.Status.LoadBalancer.Ingress) == 0 {
				return controller.Provisioning("Waiting for LoadBalancer ingress"), nil
			}
			waitingIng, err := waitingForS3Ingress(c, sw)
			if err != nil {
				return controller.Status{}, err
			}
			if waitingIng {
				return controller.Provisioning("Waiting for S3 Ingress"), nil
			}

			creds, err := readS3Credentials(c)
			if err != nil {
				return controller.Status{}, err
			}
			if !creds.complete() {
				return controller.Provisioning("Waiting for S3 credentials Secret " + s3ConfigSecretName(c.Name())), nil
			}

			details, err := buildConnectionDetailsFromService(c, svc, sw, s3Params)
			if err != nil {
				return controller.Status{}, err
			}
			details = withS3Credentials(details, creds)
			expansion, err := storageExpansionStatus(c, sw)
			if err != nil {
				return controller.Status{}, err
			}
			if expansion != nil {
				expansion.ConnectionDetails = details
				return *expansion, nil
			}
			return controller.ReadyWithConnectionDetails(details), nil
		}
		if msg, err := adminNotReadyMessage(c, sw); err != nil {
			return controller.Status{}, err
		} else if msg != "" {
			return controller.Provisioning(msg), nil
		}
		return controller.Provisioning(cond.Message), nil
	}
	if msg, err := adminNotReadyMessage(c, sw); err != nil {
		return controller.Status{}, err
	} else if msg != "" {
		return controller.Provisioning(msg), nil
	}

	if sw.Status.Master.Replicas > 0 {
		return controller.Provisioning(fmt.Sprintf(
			"Master: (%d/%d ready), Volume: (%d/%d ready), Filer: (%d/%d ready), S3: (%d/%d ready)",
			sw.Status.Master.ReadyReplicas, sw.Status.Master.Replicas,
			sw.Status.Volume.ReadyReplicas, sw.Status.Volume.Replicas,
			sw.Status.Filer.ReadyReplicas, sw.Status.Filer.Replicas,
			sw.Status.S3.ReadyReplicas, sw.Status.S3.Replicas,
		)), nil
	}

	return controller.Initializing("waiting for SeaweedFS cluster to initialize"), nil
}

// Cleanup handles deletion of provider-managed resources.
//
// Called when the Instance has a deletion timestamp set.
// Delete any resources that are not automatically cleaned up
// via owner references.
func (p *Provider) Cleanup(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up instance", "name", c.Name())

	// Resources with owner references set via c.Apply() are garbage
	// collected, the scrape resources included. The VictoriaMetrics Operator's
	// copies of the PodMonitor and of seaweedfs-operator's ServiceMonitors
	// have none.
	if err := deleteConvertedPodMonitor(c); err != nil {
		return err
	}
	return deleteConvertedServiceMonitors(c)
}
