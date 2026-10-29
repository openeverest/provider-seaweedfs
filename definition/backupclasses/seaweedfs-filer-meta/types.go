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

// Package seaweedfsfilermeta contains the schema-bearing Go types for the
// "seaweedfs-filer-meta" BackupClass. Each struct here is converted to an OpenAPI
// v3 schema by `provider-sdk generate` and inlined into the generated
// BackupClass manifest.
//
// +k8s:openapi-gen=true
package seaweedfsfilermeta

// SeaweedfsFilerMetaBackupParameters describes Backup.spec.parameters and
// Instance schedule parameters for this class. filerPath is the only field
// that changes the snapshot; it maps to SeaweedBackupSpec.FilerPath.
type SeaweedfsFilerMetaBackupParameters struct {
	// FilerPath is the filer subtree to snapshot. Defaults to "/".
	// +optional
	// +kubebuilder:default:="/"
	// +kubebuilder:validation:Pattern=`^/[A-Za-z0-9._/-]*$`
	FilerPath string `json:"filerPath,omitempty"`
}

// SeaweedfsFilerMetaRestoreParameters describes Restore.spec.parameters for
// this class. Restore-time options are not exposed yet.
type SeaweedfsFilerMetaRestoreParameters struct{}
