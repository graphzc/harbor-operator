/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionReady is the condition type set on every Harbor resource once it
// has been reconciled against the Harbor API.
const ConditionReady = "Ready"

// Finalizer is added to every Harbor-backed resource so the remote object can
// be cleaned up before the Kubernetes object is removed.
const Finalizer = "harbor.graphzc.io/finalizer"

// HarborConnectionReference points to a HarborConnection in the same namespace.
type HarborConnectionReference struct {
	// Name of the HarborConnection.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// SecretKeySelector selects a key of a Secret in the same namespace.
type SecretKeySelector struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key within the Secret. Defaults to "password".
	// +kubebuilder:default=password
	// +optional
	Key string `json:"key,omitempty"`
}

// GetKey returns the selected key, falling back to "password".
func (s SecretKeySelector) GetKey() string {
	if s.Key == "" {
		return "password"
	}
	return s.Key
}

// DeletionPolicy controls what happens to the Harbor object when the
// Kubernetes resource is deleted.
// +kubebuilder:validation:Enum=Delete;Orphan
type DeletionPolicy string

const (
	// DeletionPolicyDelete removes the object from Harbor.
	DeletionPolicyDelete DeletionPolicy = "Delete"
	// DeletionPolicyOrphan leaves the object in Harbor.
	DeletionPolicyOrphan DeletionPolicy = "Orphan"
)

// GeneratedSecret describes the Secret the operator creates to hold an
// auto-generated credential.
type GeneratedSecret struct {
	// Name of the Secret to create. Defaults to "<resource-name>-harbor-credentials".
	// +optional
	Name string `json:"name,omitempty"`
}

// HarborResourceStatus is embedded in the status of every Harbor-backed resource.
type HarborResourceStatus struct {
	// ObservedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the resource state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
