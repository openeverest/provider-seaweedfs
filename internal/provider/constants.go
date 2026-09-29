package provider

const DefaultMasterVolumeSizeLimitMB int32 = 1024
const DefaultVolumeServerDiskCount int32 = 1
const DefaultMaxVolumeCounts int32 = 8

// MaxS3Port is the highest allowed S3 HTTP port. weed s3 listens for gRPC on
// port + 10000 (seaweedv1.GRPCPortDelta), so anything above 55535 crash-loops.
const MaxS3Port int32 = 55535
