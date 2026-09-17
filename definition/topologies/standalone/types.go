// Package standalone contains custom spec types for the standalone topology.
//
// Add fields to StandaloneTopologyConfig and reference it via configSchema in
// topology.yaml when this topology needs custom configuration.
//
// +k8s:openapi-gen=true
package standalone

// StandaloneTopologyConfig defines configuration for the standalone topology.
// Add fields here when the standalone topology needs custom configuration
// beyond what the base Instance spec provides.
//
// Example:
//
//	type StandaloneTopologyConfig struct {
//	    NumShards int32 `json:"numShards,omitempty"`
//	}
//
// Then reference it in topology.yaml:
//
//	config:
//	  parametersSchema: StandaloneTopologyConfig
type StandaloneTopologyConfig struct {
	VolumeServerDiskCount *int32 `json:"volumeServerDiskCount,omitempty"`
}
