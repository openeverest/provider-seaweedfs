package provider

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
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
			},
			WatchConfigs: []controller.WatchConfig{
				controller.WatchOwned(&seaweedv1.Seaweed{}),
				// The operator does not touch the Seaweed CR when LB ingress or NodePort
				// changes, so watch the S3 Service directly to refresh status.
				controller.WatchExternal(&corev1.Service{},
					handler.EnqueueRequestsFromMapFunc(s3ServiceToInstance)),
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

// Validate checks if the Instance spec is valid.
//
// Add your provider-specific validation logic here.
// Return an error if the spec is invalid.
func (p *Provider) Validate(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Validating instance", "name", c.Name())

	master, isMasterPresent := c.Instance().Spec.Components[common.ComponentMaster]
	if err := validateMaster(master, isMasterPresent); err != nil {
		return err
	}

	volume, isVolumePresent := c.Instance().Spec.Components[common.ComponentVolume]
	if err := validateVolume(volume, isVolumePresent); err != nil {
		return err
	}

	filer, isFilerPresent := c.Instance().Spec.Components[common.ComponentFiler]
	if err := validateFiler(filer, isFilerPresent); err != nil {
		return err
	}

	s3, isS3Present := c.Instance().Spec.Components[common.ComponentS3]
	if err := validateS3(s3, isS3Present); err != nil {
		return err
	}

	var masterCustomSpec components.MasterCustomSpec
	if c.TryDecodeComponentParameters(master, &masterCustomSpec) {
		if err := validateMasterParameters(masterCustomSpec); err != nil {
			return err
		}
	}

	var volumeCustomSpec components.VolumeCustomSpec
	if c.TryDecodeComponentParameters(volume, &volumeCustomSpec) {
		if err := validateVolumeParameters(volumeCustomSpec); err != nil {
			return err
		}
	}

	var filerCustomSpec components.FilerCustomSpec
	if c.TryDecodeComponentParameters(filer, &filerCustomSpec) {
		if err := validateFilerParameters(filerCustomSpec); err != nil {
			return err
		}
	}

	var s3CustomSpec components.S3CustomSpec
	if c.TryDecodeComponentParameters(s3, &s3CustomSpec) {
		if err := validateS3Parameters(s3CustomSpec); err != nil {
			return err
		}
	}

	var topo standalone.StandaloneTopologyConfig
	if c.TryDecodeTopologyParameters(&topo) {
		if err := validateTopologyParameters(topo); err != nil {
			return err
		}
	}

	return nil
}

func validateTopologyParameters(topo standalone.StandaloneTopologyConfig) error {
	if topo.VolumeServerDiskCount != nil && *topo.VolumeServerDiskCount < 1 {
		return fmt.Errorf("volumeServerDiskCount must be at least 1")
	}
	return nil
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

	sw := &seaweedv1.Seaweed{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: seaweedv1.SeaweedSpec{
			Image:                 image,
			VolumeServerDiskCount: pointer.ToInt32(DefaultVolumeServerDiskCount),
			Master:                buildMasterSpec(master, masterCustomSpec),
			Volume:                buildVolumeSpec(volume, volumeCustomSpec),
			Filer:                 buildFilerSpec(filer, filerCustomSpec),
			S3:                    buildS3Spec(s3, s3CustomSpec),
		},
	}

	if topo.VolumeServerDiskCount != nil {
		sw.Spec.VolumeServerDiskCount = topo.VolumeServerDiskCount
	}
	labelPods(c, sw)
	applyScheduling(c, sw)

	return c.Apply(sw)
}

// labelPods labels every component's pods so the runtime counts them into
// the Instance's status.components. The operator adds them to the pod
// templates only, never to the StatefulSet/Deployment selectors.
func labelPods(c *controller.Context, sw *seaweedv1.Seaweed) {
	sw.Spec.Master.Labels = c.PodLabels(common.ComponentMaster)
	sw.Spec.Volume.Labels = c.PodLabels(common.ComponentVolume)
	sw.Spec.Filer.Labels = c.PodLabels(common.ComponentFiler)
	sw.Spec.S3.Labels = c.PodLabels(common.ComponentS3)
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

	if cond := meta.FindStatusCondition(sw.Status.Conditions, "Ready"); cond != nil {
		if cond.Status == metav1.ConditionTrue {
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
			details, err := buildConnectionDetailsFromService(c, svc)
			if err != nil {
				return controller.Status{}, err
			}
			return controller.ReadyWithConnectionDetails(details), nil
		}
		return controller.Provisioning(cond.Message), nil
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

	// TODO: Implement cleanup logic if needed.
	// Resources with owner references set via c.Apply() are automatically
	// garbage collected. Only implement this if you need custom cleanup.
	return nil
}
