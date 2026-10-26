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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
)

const (
	s3ConfigSecretSuffix = "-s3-config"

	// s3ConfigKey holds the identities file passed to `weed s3 -config`.
	s3ConfigKey = "s3.json"

	// Key names match what OpenEverest BackupStorage reads from its
	// credentials Secret, so both Secrets can be referenced directly.
	s3AccessKeyIDKey     = "AWS_ACCESS_KEY_ID"
	s3SecretAccessKeyKey = "AWS_SECRET_ACCESS_KEY"

	s3AdminIdentityName = "admin"

	// s3ConfigChecksumAnnotation rolls the S3 gateway pods when the identities
	// file changes: `weed s3` reads -config only at startup.
	s3ConfigChecksumAnnotation = "seaweedfs.openeverest.io/s3-config-checksum"

	s3AccessKeyIDLength     = 20
	s3SecretAccessKeyLength = 40

	s3AccessKeyIDAlphabet     = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	s3SecretAccessKeyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// s3AdminActions grants the admin identity full access to every bucket.
var s3AdminActions = []string{"Admin", "Read", "List", "Tagging", "Write"}

func s3ConfigSecretName(instanceName string) string {
	return instanceName + s3ConfigSecretSuffix
}

type s3Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

func (cr s3Credentials) complete() bool {
	return cr.AccessKeyID != "" && cr.SecretAccessKey != ""
}

// s3IdentitiesConfig mirrors the subset of SeaweedFS iam_pb.S3ApiConfiguration
// JSON that `weed s3 -config` needs.
type s3IdentitiesConfig struct {
	Identities []s3Identity `json:"identities"`
}

type s3Identity struct {
	Name        string         `json:"name"`
	Credentials []s3Credential `json:"credentials"`
	Actions     []string       `json:"actions"`
}

type s3Credential struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

func buildS3IdentitiesConfig(creds s3Credentials) ([]byte, error) {
	return json.Marshal(s3IdentitiesConfig{
		Identities: []s3Identity{{
			Name: s3AdminIdentityName,
			Credentials: []s3Credential{{
				AccessKey: creds.AccessKeyID,
				SecretKey: creds.SecretAccessKey,
			}},
			Actions: s3AdminActions,
		}},
	})
}

// readS3Credentials returns the admin key pair stored in the S3 config Secret,
// or empty credentials when the Secret does not exist yet.
func readS3Credentials(c *controller.Context) (s3Credentials, error) {
	secret := &corev1.Secret{}
	if err := c.Get(secret, s3ConfigSecretName(c.Name())); err != nil {
		if controller.IsNotFound(err) {
			return s3Credentials{}, nil
		}
		return s3Credentials{}, fmt.Errorf("reading S3 config Secret: %w", err)
	}
	return s3Credentials{
		AccessKeyID:     string(secret.Data[s3AccessKeyIDKey]),
		SecretAccessKey: string(secret.Data[s3SecretAccessKeyKey]),
	}, nil
}

// syncS3ConfigSecret applies the Secret holding the admin S3 identity and
// returns the checksum of the identities file. Existing keys are kept, so a
// Secret created before the Instance supplies user-chosen credentials; any
// missing key is generated.
func syncS3ConfigSecret(c *controller.Context) (string, error) {
	creds, err := readS3Credentials(c)
	if err != nil {
		return "", err
	}
	if creds.AccessKeyID == "" {
		if creds.AccessKeyID, err = randomString(s3AccessKeyIDLength, s3AccessKeyIDAlphabet); err != nil {
			return "", fmt.Errorf("generating S3 access key: %w", err)
		}
	}
	if creds.SecretAccessKey == "" {
		if creds.SecretAccessKey, err = randomString(s3SecretAccessKeyLength, s3SecretAccessKeyAlphabet); err != nil {
			return "", fmt.Errorf("generating S3 secret key: %w", err)
		}
	}

	config, err := buildS3IdentitiesConfig(creds)
	if err != nil {
		return "", fmt.Errorf("building S3 identities config: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: c.ObjectMeta(s3ConfigSecretName(c.Name())),
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			s3AccessKeyIDKey:     []byte(creds.AccessKeyID),
			s3SecretAccessKeyKey: []byte(creds.SecretAccessKey),
			s3ConfigKey:          config,
		},
	}
	if err := c.Apply(secret); err != nil {
		return "", fmt.Errorf("applying S3 config Secret: %w", err)
	}

	sum := sha256.Sum256(config)
	return hex.EncodeToString(sum[:]), nil
}

// applyS3Auth points the S3 gateway at the identities file. A gateway started
// with -config enforces authentication from its first request.
func applyS3Auth(spec *seaweedv1.S3GatewaySpec, instanceName, configChecksum string) {
	spec.ConfigSecret = &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: s3ConfigSecretName(instanceName)},
		Key:                  s3ConfigKey,
	}
	if spec.Annotations == nil {
		spec.Annotations = map[string]string{}
	}
	spec.Annotations[s3ConfigChecksumAnnotation] = configChecksum
}

// withS3Credentials adds the admin key pair to the connection details.
// username/password are what the OpenEverest UI shows; the AWS_* keys let a
// BackupStorage reference the connection Secret directly.
func withS3Credentials(details controller.ConnectionDetails, creds s3Credentials) controller.ConnectionDetails {
	details.Username = creds.AccessKeyID
	details.Password = creds.SecretAccessKey
	if details.AdditionalProperties == nil {
		details.AdditionalProperties = map[string]string{}
	}
	details.AdditionalProperties[s3AccessKeyIDKey] = creds.AccessKeyID
	details.AdditionalProperties[s3SecretAccessKeyKey] = creds.SecretAccessKey
	return details
}

func randomString(length int, alphabet string) (string, error) {
	limit := big.NewInt(int64(len(alphabet)))
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}
