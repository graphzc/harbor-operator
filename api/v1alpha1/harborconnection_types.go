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

// HarborConnectionSpec defines how the operator reaches a Harbor instance.
type HarborConnectionSpec struct {
	// URL of the Harbor instance, e.g. https://harbor.example.com.
	// +kubebuilder:validation:Pattern=`^https?://.+`
	URL string `json:"url"`

	// Username of a Harbor system administrator. Defaults to "admin".
	// +kubebuilder:default=admin
	// +optional
	Username string `json:"username,omitempty"`

	// PasswordSecretRef selects the Secret key holding the administrator password.
	PasswordSecretRef SecretKeySelector `json:"passwordSecretRef"`

	// InsecureSkipVerify disables TLS certificate verification.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CASecretRef selects a Secret key holding a PEM-encoded CA bundle used to
	// verify the Harbor TLS certificate.
	// +optional
	CASecretRef *SecretKeySelector `json:"caSecretRef,omitempty"`
}

// HarborConnectionStatus defines the observed state of HarborConnection.
type HarborConnectionStatus struct {
	HarborResourceStatus `json:",inline"`

	// HarborVersion reported by the Harbor system info API.
	// +optional
	HarborVersion string `json:"harborVersion,omitempty"`

	// AuthMode reported by Harbor (e.g. db_auth, ldap_auth, oidc_auth).
	// +optional
	AuthMode string `json:"authMode,omitempty"`

	// LastCheckedTime is when connectivity was last verified.
	// +optional
	LastCheckedTime *metav1.Time `json:"lastCheckedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hc
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.harborVersion`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HarborConnection connects the cluster to a Harbor instance. Other Harbor
// resources reference it to know which Harbor to manage.
type HarborConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HarborConnectionSpec   `json:"spec,omitempty"`
	Status HarborConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HarborConnectionList contains a list of HarborConnection.
type HarborConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HarborConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HarborConnection{}, &HarborConnectionList{})
}
