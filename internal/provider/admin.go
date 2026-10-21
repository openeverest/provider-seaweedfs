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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

const (
	// Secret keys seaweedfs-operator 1.0.40 reads from the mounted Secret
	// and passes to weed admin as flags. adminPassword is the one the
	// startup script refuses to start without. adminUser is optional there;
	// weed's own default is "admin". readOnlyUser and readOnlyPassword are
	// optional.
	adminUserKey         = "adminUser"
	adminPasswordKey     = "adminPassword"
	adminReadOnlyUserKey = "readOnlyUser"
	adminReadOnlyPassKey = "readOnlyPassword"
)

func buildAdminSpec(topo standalone.StandaloneTopologyConfig) *seaweedv1.AdminSpec {
	if topo.Admin == nil || topo.Admin.CredentialsSecret == "" {
		return nil
	}
	return &seaweedv1.AdminSpec{
		CredentialsSecret: &corev1.LocalObjectReference{Name: topo.Admin.CredentialsSecret},
	}
}

func validateAdminParameters(topo standalone.StandaloneTopologyConfig) error {
	if topo.Admin == nil || topo.Admin.CredentialsSecret == "" {
		return nil
	}
	name := topo.Admin.CredentialsSecret
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("admin.credentialsSecret %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func adminNotReadyMessage(c *controller.Context, sw *seaweedv1.Seaweed) (string, error) {
	if sw.Spec.Admin == nil || sw.Spec.Admin.CredentialsSecret == nil || sw.Spec.Admin.CredentialsSecret.Name == "" {
		return "", nil
	}
	name := sw.Spec.Admin.CredentialsSecret.Name
	secret := &corev1.Secret{}
	if err := c.Get(secret, name); err != nil {
		if controller.IsNotFound(err) {
			return fmt.Sprintf(
				"Waiting for admin credentials Secret %q (keys %s and %s; optional %s and %s)",
				name, adminUserKey, adminPasswordKey, adminReadOnlyUserKey, adminReadOnlyPassKey,
			), nil
		}
		return "", err
	}
	if len(secret.Data[adminPasswordKey]) == 0 {
		return fmt.Sprintf("Admin credentials Secret %q is missing the %s key", name, adminPasswordKey), nil
	}
	return "", nil
}
