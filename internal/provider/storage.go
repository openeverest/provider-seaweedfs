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
	"errors"
	"fmt"
	"strconv"
	"strings"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	seaweedv1 "github.com/seaweedfs/seaweedfs-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/provider-seaweedfs/internal/common"
)

// validateStorageExpansion rejects a smaller storage.size, enabling master
// persistence after creation, and a larger size whose applied StorageClass
// does not set allowVolumeExpansion. seaweedfs-operator image 1.0.40 patches
// live PVCs only when the claim template differs in the storage request
// (pvcEqualExceptStorageRequests). A class change emits
// VolumeClaimTemplatesMismatch and leaves the PVCs unchanged. Adding master
// persistence deletes and recreates the master StatefulSet. A shrink is
// ignored by the operator. A class that disallows expansion leaves the PVC
// unchanged (Warning event VolumeExpansionNotSupported). StatefulSet
// volumeClaimTemplates stay immutable either way.
func validateStorageExpansion(c *controller.Context, sw *seaweedv1.Seaweed) error {
	for _, component := range persistentComponents {
		comp, found := c.Instance().Spec.Components[component]
		if !found {
			continue
		}
		applied, ok := appliedStorage(sw, component)
		if !ok {
			// Volume and filer always have a claim. Master persistence is
			// optional, turning it on later makes the operator delete the
			// master StatefulSet and restart every master at once.
			if component == common.ComponentMaster && comp.Storage != nil && !comp.Storage.Size.IsZero() {
				return fmt.Errorf("%q component: storage cannot be enabled after creation", component)
			}
			continue
		}
		desired := resource.Quantity{}
		if comp.Storage != nil {
			desired = comp.Storage.Size
		}
		switch desired.Cmp(applied.size) {
		case 0:
			continue
		case -1:
			return fmt.Errorf("%q component: storage.size cannot shrink from %s to %s", component, applied.size.String(), desired.String())
		}
		// The operator only expands PVCs when the claim template differs in
		// size alone, so the class must stay as applied.
		if comp.Storage != nil && comp.Storage.StorageClass != nil && *comp.Storage.StorageClass != "" &&
			(applied.class == nil || *applied.class != *comp.Storage.StorageClass) {
			return fmt.Errorf("%q component: storage.storageClass cannot change while storage.size grows", component)
		}
		className := ""
		if applied.class != nil {
			className = *applied.class
		}
		if err := requireExpansionAllowed(c, component, className); err != nil {
			return err
		}
	}
	return nil
}

// storageExpansionStatus reports PVC growth after the Seaweed cluster is Ready.
// The operator does not copy expansion onto Seaweed status. Progress is the
// PVC Resizing condition. FileSystemResizePending is ignored: with offline
// expansion it stays until a pod restart that neither the operator nor the
// provider performs. A class that disallows expansion never changes the PVC
// request.
func storageExpansionStatus(c *controller.Context, sw *seaweedv1.Seaweed) (*controller.Status, error) {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := c.Client().List(c.Context(), pvcs, client.InNamespace(c.Namespace())); err != nil {
		return nil, err
	}

	var waiting, blocked []string
	for _, component := range persistentComponents {
		comp, ok := c.Instance().Spec.Components[component]
		if !ok || comp.Storage == nil || comp.Storage.Size.IsZero() {
			continue
		}
		target := comp.Storage.Size
		for i := range pvcs.Items {
			pvc := &pvcs.Items[i]
			if !pvcMatchesClaim(pvc.Name, claimPrefixes(c.Name(), component, sw)) {
				continue
			}
			if msg, resizing := pvcResizeMessage(component, pvc); resizing {
				waiting = append(waiting, msg)
				continue
			}
			req := pvc.Spec.Resources.Requests.Storage()
			if req == nil || target.Cmp(*req) <= 0 {
				continue
			}
			className := ""
			if pvc.Spec.StorageClassName != nil {
				className = *pvc.Spec.StorageClassName
			}
			if className == "" {
				if applied, ok := appliedStorage(sw, component); ok && applied.class != nil {
					className = *applied.class
				}
			}
			allowed, resolved, err := lookupExpansion(c, className)
			if err != nil {
				if !errors.Is(err, errStorageClassMissing) {
					return nil, fmt.Errorf("%q component: %w", component, err)
				}
				blocked = append(blocked, fmt.Sprintf("%s PVC %s: storage class %q does not exist", component, pvc.Name, resolved))
				continue
			}
			if !allowed {
				blocked = append(blocked, expansionDenied(component, pvc.Name, resolved))
				continue
			}
			waiting = append(waiting, fmt.Sprintf("waiting for %s PVC %s to expand from %s to %s", component, pvc.Name, req.String(), target.String()))
		}
	}
	if len(blocked) == 0 && len(waiting) == 0 {
		return nil, nil
	}
	if len(blocked) > 0 {
		return &controller.Status{
			Phase:   corev1alpha1.InstancePhaseFailed,
			Message: strings.Join(append(blocked, waiting...), "; "),
		}, nil
	}
	return &controller.Status{
		Phase:   corev1alpha1.InstancePhaseUpdating,
		Message: strings.Join(waiting, "; "),
	}, nil
}

var persistentComponents = []string{common.ComponentVolume, common.ComponentFiler, common.ComponentMaster}

type componentStorage struct {
	size  resource.Quantity
	class *string
}

func appliedStorage(sw *seaweedv1.Seaweed, component string) (componentStorage, bool) {
	switch component {
	case common.ComponentVolume:
		if sw.Spec.Volume == nil {
			return componentStorage{}, false
		}
		q, ok := sw.Spec.Volume.Requests[corev1.ResourceStorage]
		if !ok || q.IsZero() {
			return componentStorage{}, false
		}
		return componentStorage{size: q, class: sw.Spec.Volume.StorageClassName}, true
	case common.ComponentFiler:
		return persistenceStorage(sw.Spec.Filer)
	case common.ComponentMaster:
		if sw.Spec.Master == nil {
			return componentStorage{}, false
		}
		return persistenceStorageSpec(sw.Spec.Master.Persistence)
	default:
		return componentStorage{}, false
	}
}

func persistenceStorage(filer *seaweedv1.FilerSpec) (componentStorage, bool) {
	if filer == nil {
		return componentStorage{}, false
	}
	return persistenceStorageSpec(filer.Persistence)
}

func persistenceStorageSpec(p *seaweedv1.PersistenceSpec) (componentStorage, bool) {
	if p == nil || !p.Enabled {
		return componentStorage{}, false
	}
	q, ok := p.Resources.Requests[corev1.ResourceStorage]
	if !ok || q.IsZero() {
		return componentStorage{}, false
	}
	return componentStorage{size: q, class: p.StorageClassName}, true
}

func requireExpansionAllowed(c *controller.Context, component, className string) error {
	allowed, resolved, err := lookupExpansion(c, className)
	if errors.Is(err, errStorageClassMissing) {
		return fmt.Errorf("%q component: storage class %q does not exist", component, resolved)
	}
	if err != nil {
		return fmt.Errorf("%q component: %w", component, err)
	}
	if !allowed {
		if resolved == "" {
			return fmt.Errorf("%q component: no StorageClass allows volume expansion", component)
		}
		return fmt.Errorf("%q component: storage class %q does not allow volume expansion", component, resolved)
	}
	return nil
}

func expansionDenied(component, pvcName, className string) string {
	if className == "" {
		return fmt.Sprintf("%s PVC %s: no StorageClass allows volume expansion", component, pvcName)
	}
	return fmt.Sprintf("%s PVC %s: storage class %q does not allow volume expansion", component, pvcName, className)
}

var errStorageClassMissing = errors.New("storage class does not exist")

// lookupExpansion resolves className, or the newest default StorageClass when
// className is empty. A missing named class returns errStorageClassMissing.
// No default class is allowed=false and an empty resolved name.
func lookupExpansion(c *controller.Context, className string) (bool, string, error) {
	if className == "" {
		resolved, err := defaultStorageClassName(c)
		if err != nil {
			return false, "", err
		}
		if resolved == "" {
			return false, "", nil
		}
		className = resolved
	}
	sc := &unstructured.Unstructured{}
	sc.SetGroupVersionKind(storageClassGVK)
	if err := c.Client().Get(c.Context(), client.ObjectKey{Name: className}, sc); err != nil {
		if controller.IsNotFound(err) {
			return false, className, fmt.Errorf("storage class %q: %w", className, errStorageClassMissing)
		}
		return false, className, fmt.Errorf("checking storage class %q: %w", className, err)
	}
	allowed, _, err := unstructured.NestedBool(sc.Object, "allowVolumeExpansion")
	if err != nil {
		return false, className, fmt.Errorf("checking storage class %q: %w", className, err)
	}
	return allowed, className, nil
}

func defaultStorageClassName(c *controller.Context) (string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   storageClassGVK.Group,
		Version: storageClassGVK.Version,
		Kind:    "StorageClassList",
	})
	if err := c.Client().List(c.Context(), list); err != nil {
		return "", fmt.Errorf("listing storage classes: %w", err)
	}
	var newest *unstructured.Unstructured
	for i := range list.Items {
		sc := &list.Items[i]
		if !isDefaultStorageClass(sc) {
			continue
		}
		if newest == nil || sc.GetCreationTimestamp().After(newest.GetCreationTimestamp().Time) {
			newest = sc
		}
	}
	if newest == nil {
		return "", nil
	}
	return newest.GetName(), nil
}

func isDefaultStorageClass(sc *unstructured.Unstructured) bool {
	ann := sc.GetAnnotations()
	return ann["storageclass.kubernetes.io/is-default-class"] == "true" ||
		ann["storageclass.beta.kubernetes.io/is-default-class"] == "true"
}

// claimPrefixes are the PVC name prefixes seaweedfs-operator uses:
// "<claim>-<statefulset>-". Volume claims are mountN; filer and master
// claims are named "<instance>-filer" and "<instance>-master".
func claimPrefixes(instance, component string, sw *seaweedv1.Seaweed) []string {
	switch component {
	case common.ComponentVolume:
		disks := int32(1)
		if sw.Spec.VolumeServerDiskCount != nil && *sw.Spec.VolumeServerDiskCount > 0 {
			disks = *sw.Spec.VolumeServerDiskCount
		}
		prefixes := make([]string, 0, disks)
		for i := int32(0); i < disks; i++ {
			prefixes = append(prefixes, fmt.Sprintf("mount%d-%s-volume-", i, instance))
		}
		return prefixes
	case common.ComponentFiler:
		return []string{fmt.Sprintf("%s-filer-%s-filer-", instance, instance)}
	case common.ComponentMaster:
		return []string{fmt.Sprintf("%s-master-%s-master-", instance, instance)}
	default:
		return nil
	}
}

func pvcMatchesClaim(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(name, prefix)); err == nil {
			return true
		}
	}
	return false
}

func pvcResizeMessage(component string, pvc *corev1.PersistentVolumeClaim) (string, bool) {
	for _, cond := range pvc.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		// FileSystemResizePending is not tracked: with offline-only drivers it
		// stays until a pod restart that neither the operator nor the provider
		// performs, which would pin the Instance in Updating.
		if cond.Type != corev1.PersistentVolumeClaimResizing {
			continue
		}
		msg := fmt.Sprintf("%s PVC %s: %s", component, pvc.Name, cond.Type)
		if cond.Message != "" {
			msg += ": " + cond.Message
		}
		return msg, true
	}
	return "", false
}
