package provider

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
			},
		},
	}
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
	if err := validateRequiredComponent(common.ComponentVolume, volume, isVolumePresent); err != nil {
		return err
	}

	filer, isFilerPresent := c.Instance().Spec.Components[common.ComponentFiler]
	if err := validateRequiredComponent(common.ComponentFiler, filer, isFilerPresent); err != nil {
		return err
	}

	s3, isS3Present := c.Instance().Spec.Components[common.ComponentS3]
	if err := validateRequiredComponent(common.ComponentS3, s3, isS3Present); err != nil {
		return err
	}

	var topo standalone.StandaloneTopologyConfig
	if c.TryDecodeTopologyParameters(&topo) {
		if err := c.DecodeTopologyParameters(&topo); err != nil {
			return fmt.Errorf("failed to decode topology parameters: %w", err)
		}
		if err := validateTopologyParameters(topo); err != nil {
			return err
		}
	}

	var masterCustomSpec components.MasterCustomSpec
	if c.TryDecodeComponentParameters(master, &masterCustomSpec) {
		if err := c.DecodeComponentParameters(master, &masterCustomSpec); err != nil {
			return fmt.Errorf("failed to decode master component parameters: %w", err)
		}
		if err := validateMasterParameters(masterCustomSpec); err != nil {
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

	var topo standalone.StandaloneTopologyConfig
	if c.TryDecodeTopologyParameters(&topo) {
		if err := c.DecodeTopologyParameters(&topo); err != nil {
			return fmt.Errorf("failed to decode topology parameters: %w", err)
		}
	}

	var masterCustomSpec components.MasterCustomSpec
	if c.TryDecodeComponentParameters(master, &masterCustomSpec) {
		if err := c.DecodeComponentParameters(master, &masterCustomSpec); err != nil {
			return fmt.Errorf("failed to decode master component parameters: %w", err)
		}
	}

	image, err := resolveImage(c, common.ComponentMaster, master)
	if err != nil {
		return err
	}

	sw := &seaweedv1.Seaweed{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: seaweedv1.SeaweedSpec{
			Image:                 image,
			VolumeServerDiskCount: pointer.ToInt32(DefaultVolumeServerDiskCount),
			Master: buildMasterSpec(master, masterCustomSpec),
			Volume: &seaweedv1.VolumeSpec{Replicas: *volume.Replicas},
			Filer:	&seaweedv1.FilerSpec{Replicas: *filer.Replicas},
			S3: 	&seaweedv1.S3GatewaySpec{Replicas: *s3.Replicas},
		},
	}

	if volume.Storage != nil {
		sw.Spec.Volume.Requests = corev1.ResourceList{
			corev1.ResourceStorage: volume.Storage.Size,
		}
	}

	if topo.VolumeServerDiskCount != nil {
		sw.Spec.VolumeServerDiskCount = topo.VolumeServerDiskCount
	}

	return c.Apply(sw)
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
			return controller.Ready(), nil
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
