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

const DefaultMasterVolumeSizeLimitMB int32 = 1024
const DefaultVolumeServerDiskCount int32 = 1

// DefaultVolumeGrowthCount is how many logical volumes the master creates at
// once when a collection (one per S3 bucket) needs a writable volume. Upstream
// defaults (7 for 000, 6 for 001, 3 for 002) reserve up to 12 volume slots per
// bucket, which exhausts small clusters after a couple of buckets.
const DefaultVolumeGrowthCount int32 = 2

// DefaultReplication is SeaweedFS master.defaultReplication when the user
// leaves topology.parameters.replication empty and at least two volume
// servers are requested. 001 is one extra copy on the same rack.
// A single volume server falls back to 000, which is the only placement
// that fits one server in one rack and one data center.
const DefaultReplication = "001"

// MaxS3Port is the highest allowed S3 HTTP port. weed s3 listens for gRPC on
// port + 10000 (seaweedv1.GRPCPortDelta), so anything above 55535 crash-loops.
const MaxS3Port int32 = 55535
