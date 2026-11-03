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

// GroupType is the kind of Harbor user group.
// +kubebuilder:validation:Enum=LDAP;HTTP;OIDC
type GroupType string

const (
	GroupTypeLDAP GroupType = "LDAP"
	GroupTypeHTTP GroupType = "HTTP"
	GroupTypeOIDC GroupType = "OIDC"
)

// GroupSpec defines the desired state of a Harbor user group.
// +kubebuilder:validation:XValidation:rule="self.groupType == oldSelf.groupType",message="groupType is immutable"
// +kubebuilder:validation:XValidation:rule="self.groupType != 'LDAP' || has(self.ldapGroupDN)",message="ldapGroupDN is required for LDAP groups"
type GroupSpec struct {
	// HarborConnectionRef selects the Harbor instance.
	HarborConnectionRef HarborConnectionReference `json:"harborConnectionRef"`

	// GroupName in Harbor.
	// +kubebuilder:validation:MinLength=1
	GroupName string `json:"groupName"`

	// GroupType of the group. Immutable.
	// +kubebuilder:default=HTTP
	// +optional
	GroupType GroupType `json:"groupType,omitempty"`

	// LDAPGroupDN is the distinguished name of an LDAP group.
	// +optional
	LDAPGroupDN string `json:"ldapGroupDN,omitempty"`

	// DeletionPolicy controls whether the Harbor group is removed with this resource.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// GroupStatus defines the observed state of Group.
type GroupStatus struct {
	HarborResourceStatus `json:",inline"`

	// GroupID is the Harbor user group ID.
	// +optional
	GroupID int64 `json:"groupID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hg
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.groupName`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.groupType`
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=`.status.groupID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Group is a Harbor user group.
type Group struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GroupSpec   `json:"spec,omitempty"`
	Status GroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GroupList contains a list of Group.
type GroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Group `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Group{}, &GroupList{})
}
