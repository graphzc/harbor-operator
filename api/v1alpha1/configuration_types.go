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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConfigurationSpec defines the desired Harbor system configuration.
// Keys are the camelCase form of the Harbor configuration keys returned by
// GET /api/v2.0/configurations, e.g. projectCreationRestriction for
// project_creation_restriction. A run of capitals is one word, so ldapURL and
// ldapUrl both mean ldap_url.
type ConfigurationSpec struct {
	// HarborConnectionRef selects the Harbor instance.
	HarborConnectionRef HarborConnectionReference `json:"harborConnectionRef"`

	// Settings maps camelCase Harbor configuration keys (e.g. authMode,
	// projectCreationRestriction, robotTokenDuration) to their values. Keys
	// that are not listed are left as they are in Harbor.
	// +kubebuilder:validation:MaxProperties=256
	// +optional
	Settings map[string]apiextensionsv1.JSON `json:"settings,omitempty"`

	// SecretSettings maps camelCase Harbor configuration keys holding
	// credentials (e.g. ldapSearchPassword, oidcClientSecret) to Secret keys.
	// A key cannot also be set in settings.
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.matches('^[a-z][a-zA-Z0-9]*$'))",message="keys must be camelCase, e.g. oidcClientSecret"
	// +optional
	SecretSettings map[string]SecretKeySelector `json:"secretSettings,omitempty"`
}

// ConfigurationStatus defines the observed state of Configuration.
type ConfigurationStatus struct {
	HarborResourceStatus `json:",inline"`

	// SecretSettingsHash is a bcrypt hash of the applied secretSettings values.
	// Harbor never returns credentials, so this is how changes are detected.
	// +optional
	SecretSettingsHash string `json:"secretSettingsHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hcfg
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.spec.harborConnectionRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Configuration manages Harbor system configuration settings. Harbor has a
// single configuration, so deleting the resource leaves the settings in place.
type Configuration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigurationSpec   `json:"spec,omitempty"`
	Status ConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigurationList contains a list of Configuration.
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Configuration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Configuration{}, &ConfigurationList{})
}
