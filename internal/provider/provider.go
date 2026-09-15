package provider

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	corev1 "k8s.io/api/core/v1"

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
			SchemeFuncs:  []func(*runtime.Scheme) error{
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
//
func (p *Provider) Validate(c *controller.Context) error {
	l := log.FromContext(c.Context())
	l.Info("Validating instance", "name", c.Name())

	// TODO: Implement validation logic.
	// Examples:
	//   - Check that required components are present
	//   - Validate storage sizes, replica counts
	//   - Ensure version compatibility
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

	image, err := resolveImage(c, common.ComponentMaster, master)
	if err != nil {
		return err
	}

	sw := &seaweedv1.Seaweed{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: seaweedv1.SeaweedSpec{
			Image: image,
			//TODO: can be added via CustomSpec
			VolumeServerDiskCount: pointer.ToInt32(1),
			Master:                &seaweedv1.MasterSpec{Replicas: *master.Replicas, VolumeSizeLimitMB: pointer.ToInt32(1024)},
			Volume:                &seaweedv1.VolumeSpec{Replicas: *volume.Replicas},
			Filer:                 &seaweedv1.FilerSpec{Replicas: *filer.Replicas},
			S3:                    &seaweedv1.S3GatewaySpec{Replicas: *s3.Replicas},
		},
	}

	if volume.Storage != nil {
		sw.Spec.Volume.Requests = corev1.ResourceList{
			corev1.ResourceStorage: volume.Storage.Size,
		}
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

	// TODO: Implement status logic.
	// Typical pattern:
	//   1. Get the operator CR using c.Get()
	//   2. Translate its status to a controller.Status
	//
	// Example:
	//   cr := &operatorv1.MyDatabase{}
	//   if err := c.Get(cr, c.Name()); err != nil {
	//       return controller.Status{}, err
	//   }
	//   if cr.Status.Ready {
	//       return controller.ReadyWithConnectionDetails(
	//           controller.ConnectionDetails{
	//           // Populate connection details.
	//           },
	//       ), nil
	//   }
	//   return controller.Provisioning("waiting for database to be ready"), nil

	return controller.Provisioning("initializing"), nil
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
