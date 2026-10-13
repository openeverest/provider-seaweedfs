package provider

const DefaultMasterVolumeSizeLimitMB int32 = 1024
const DefaultVolumeServerDiskCount int32 = 1
const DefaultMaxVolumeCounts int32 = 8

// DefaultReplication is SeaweedFS master.defaultReplication when the user
// leaves topology.parameters.replication empty and at least two volume
// servers are requested. 001 is one extra copy on the same rack.
// A single volume server falls back to 000, which is the only placement
// that fits one server in one rack and one data center.
const DefaultReplication = "001"

// MaxS3Port is the highest allowed S3 HTTP port. weed s3 listens for gRPC on
// port + 10000 (seaweedv1.GRPCPortDelta), so anything above 55535 crash-loops.
const MaxS3Port int32 = 55535
