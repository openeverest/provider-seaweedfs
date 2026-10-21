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
	"crypto/rand"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/topologies/standalone"
)

const (
	// Secret keys seaweedfs-operator 1.0.40 reads from the mounted Secret
	// and passes to weed admin as flags. adminPassword is the one the
	// startup script refuses to start without.
	adminUserKey     = "adminUser"
	adminPasswordKey = "adminPassword"

	// adminUsername is the only login this provider creates.
	adminUsername = "admin"

	// Alphanumeric only: the operator startup script interpolates the
	// password inside a double-quoted shell word.
	adminPasswordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	adminPasswordLength   = 32
)

func adminEnabled(topo standalone.StandaloneTopologyConfig) bool {
	return topo.Admin != nil && topo.Admin.Enabled
}

func adminCredentialsSecretName(instanceName string) string {
	return instanceName + "-admin-credentials"
}

func adminServiceName(instanceName string) string {
	return instanceName + "-admin"
}

func buildAdminSpec(secretName string) *seaweedv1.AdminSpec {
	if secretName == "" {
		return nil
	}
	return &seaweedv1.AdminSpec{
		CredentialsSecret: &corev1.LocalObjectReference{Name: secretName},
	}
}

func ensureAdminCredentials(c *controller.Context, topo standalone.StandaloneTopologyConfig) (string, error) {
	if !adminEnabled(topo) {
		return "", nil
	}
	name := adminCredentialsSecretName(c.Name())
	password, err := existingAdminPassword(c, name)
	if err != nil {
		return "", err
	}
	if password == "" {
		password, err = generateAdminPassword()
		if err != nil {
			return "", fmt.Errorf("generating admin password: %w", err)
		}
	}
	secret := &corev1.Secret{
		ObjectMeta: c.ObjectMeta(name),
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			adminUserKey:     []byte(adminUsername),
			adminPasswordKey: []byte(password),
		},
	}
	if err := c.Apply(secret); err != nil {
		return "", fmt.Errorf("applying admin credentials Secret %q: %w", name, err)
	}
	return name, nil
}

func existingAdminPassword(c *controller.Context, name string) (string, error) {
	secret := &corev1.Secret{}
	if err := c.Get(secret, name); err != nil {
		if controller.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return string(secret.Data[adminPasswordKey]), nil
}

func generateAdminPassword() (string, error) {
	buf := make([]byte, adminPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	alphabet := []byte(adminPasswordAlphabet)
	out := make([]byte, adminPasswordLength)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
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
				"Waiting for admin credentials Secret %q (keys %s and %s)",
				name, adminUserKey, adminPasswordKey,
			), nil
		}
		return "", err
	}
	if len(secret.Data[adminPasswordKey]) == 0 {
		return fmt.Sprintf("Admin credentials Secret %q is missing the %s key", name, adminPasswordKey), nil
	}
	return "", nil
}

func addAdminConnectionDetails(c *controller.Context, sw *seaweedv1.Seaweed, props map[string]string) {
	if sw == nil || sw.Spec.Admin == nil || sw.Spec.Admin.CredentialsSecret == nil || sw.Spec.Admin.CredentialsSecret.Name == "" {
		return
	}
	ns := sw.Namespace
	if ns == "" {
		ns = c.Namespace()
	}
	props["adminURL"] = fmt.Sprintf("http://%s.%s.svc:%d", adminServiceName(sw.Name), ns, seaweedv1.AdminHTTPPort)
	props["adminUser"] = adminUsername
	props["adminCredentialsSecret"] = sw.Spec.Admin.CredentialsSecret.Name
}
