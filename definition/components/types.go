// Package components contains custom spec types for provider component types.
//
// Each struct here corresponds to a component type defined in versions.yaml
// and is converted to an OpenAPI schema during generation.
// Add fields when a component type needs custom configuration beyond
// what the base Instance spec provides.
//
// +k8s:openapi-gen=true
package components

type MasterCustomSpec struct {
	MasterVolumeSizeLimitMB *int32 `json:"masterVolumeSizeLimitMB,omitempty"`
}

type VolumeCustomSpec struct {
	MaxVolumeCounts *int32 `json:"maxVolumeCounts,omitempty"`
}
