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
	"regexp"
	"strconv"
	"strings"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"

	"github.com/openeverest/provider-seaweedfs/definition/components"
	"github.com/openeverest/provider-seaweedfs/internal/common"
)

func validateFiler(comp corev1alpha1.ComponentSpec, present bool) error {
	if err := validateRequiredComponent(common.ComponentFiler, comp, present); err != nil {
		return err
	}
	if comp.Storage == nil || comp.Storage.Size.IsZero() {
		return fmt.Errorf("%q component: storage.size is required", common.ComponentFiler)
	}
	return validateNoService(common.ComponentFiler, comp)
}

func validateFilerParameters(spec components.FilerCustomSpec) error {
	if spec.MaxMB != nil && *spec.MaxMB < 1 {
		return fmt.Errorf("maxMB must be at least 1")
	}
	return nil
}

// minMultiFilerVersion is the first SeaweedFS release whose filer persists its
// metadata change log on shutdown (seaweedfs/seaweedfs#11359). Before it, a
// filer that restarts drops up to a minute of changes from the log, and a peer
// filer that was offline at the time never receives them: S3 then returns 404
// for objects it acknowledged.
var minMultiFilerVersion = seaweedfsVersion{major: 4, minor: 48}

type seaweedfsVersion struct {
	major, minor int
}

func (v seaweedfsVersion) less(o seaweedfsVersion) bool {
	return v.major < o.major || (v.major == o.major && v.minor < o.minor)
}

func (v seaweedfsVersion) String() string {
	return fmt.Sprintf("%d.%d", v.major, v.minor)
}

var seaweedfsTagPattern = regexp.MustCompile(`^(\d+)\.(\d+)`)

// parseSeaweedfsVersion reads the release from an image tag such as
// "chrislusf/seaweedfs:4.48" or "registry:5000/seaweedfs:4.48_large_disk".
// ok is false for tags that carry no release, like "latest" or a bare digest.
func parseSeaweedfsVersion(image string) (v seaweedfsVersion, ok bool) {
	image, _, _ = strings.Cut(image, "@")
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon <= slash {
		return seaweedfsVersion{}, false
	}
	m := seaweedfsTagPattern.FindStringSubmatch(image[colon+1:])
	if m == nil {
		return seaweedfsVersion{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return seaweedfsVersion{major: major, minor: minor}, true
}

// validateFilerVersion rejects multiple filers on releases that can lose
// metadata between them. Images whose release cannot be read are allowed.
func validateFilerVersion(image string, filerReplicas int32) error {
	if filerReplicas < 2 {
		return nil
	}
	v, ok := parseSeaweedfsVersion(image)
	if !ok || !v.less(minMultiFilerVersion) {
		return nil
	}
	return fmt.Errorf("%q component: %d replicas require SeaweedFS %s or newer, got %s (%s); "+
		"older filers can lose metadata changes when a peer filer restarts",
		common.ComponentFiler, filerReplicas, minMultiFilerVersion, v, image)
}

func buildFilerSpec(comp corev1alpha1.ComponentSpec, filerCustomSpec components.FilerCustomSpec) *seaweedv1.FilerSpec {
	spec := &seaweedv1.FilerSpec{
		Replicas: *comp.Replicas,
		MaxMB:    filerCustomSpec.MaxMB,
	}

	if comp.Resources != nil {
		spec.ResourceRequirements = *comp.Resources
	}

	spec.Persistence = buildPersistenceSpec(comp.Storage)

	return spec
}
