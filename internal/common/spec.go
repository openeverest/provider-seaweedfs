// Package common defines shared constants used across the provider.
package common

const (
	// ProviderName is the canonical name of this provider.
	ProviderName = "provider-seaweedfs"

	ComponentMaster = "master"
	ComponentVolume = "volume"
	ComponentFiler  = "filer"
	ComponentS3     = "s3"
	// ComponentAdmin is not an Instance component. The provider always runs
	// one admin server; the name is only the pod label the runtime counts.
	ComponentAdmin = "admin"
)
