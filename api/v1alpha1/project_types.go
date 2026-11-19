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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectRole is a Harbor project role.
// +kubebuilder:validation:Enum=ProjectAdmin;Maintainer;Developer;Guest;LimitedGuest
type ProjectRole string

const (
	RoleProjectAdmin ProjectRole = "ProjectAdmin"
	RoleDeveloper    ProjectRole = "Developer"
	RoleGuest        ProjectRole = "Guest"
	RoleMaintainer   ProjectRole = "Maintainer"
	RoleLimitedGuest ProjectRole = "LimitedGuest"
)

// MemberKind is the kind of project member.
// +kubebuilder:validation:Enum=User;Group
type MemberKind string

const (
	MemberKindUser  MemberKind = "User"
	MemberKindGroup MemberKind = "Group"
)

// ProjectMember grants a role in the project to a user or group.
type ProjectMember struct {
	// Kind of the member.
	// +kubebuilder:default=User
	// +optional
	Kind MemberKind `json:"kind,omitempty"`

	// Name is the Harbor username or group name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Role granted in the project.
	Role ProjectRole `json:"role"`
}

// Severity is a vulnerability severity level.
// +kubebuilder:validation:Enum=none;low;medium;high;critical
type Severity string

// CVEAllowlist is a project-level CVE allowlist.
type CVEAllowlist struct {
	// Items are the allowed CVE IDs.
	// +optional
	Items []string `json:"items,omitempty"`

	// ExpiresAt is when the allowlist stops applying. Never expires when omitted.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// ProjectPolicies are the project configuration policies. Unset fields are
// left as they are in Harbor.
type ProjectPolicies struct {
	// AutoScan scans images automatically when pushed.
	// +optional
	AutoScan *bool `json:"autoScan,omitempty"`

	// AutoSBOMGeneration generates an SBOM automatically when images are pushed.
	// +optional
	AutoSBOMGeneration *bool `json:"autoSBOMGeneration,omitempty"`

	// PreventVulnerable prevents pulling images with vulnerabilities at or above Severity.
	// +optional
	PreventVulnerable *bool `json:"preventVulnerable,omitempty"`

	// Severity threshold used by PreventVulnerable.
	// +optional
	Severity Severity `json:"severity,omitempty"`

	// ContentTrust allows only verified (Notary) images to be pulled.
	// +optional
	ContentTrust *bool `json:"contentTrust,omitempty"`

	// ContentTrustCosign allows only images signed by cosign to be pulled.
	// +optional
	ContentTrustCosign *bool `json:"contentTrustCosign,omitempty"`

	// ReuseSysCVEAllowlist uses the system CVE allowlist instead of the project one.
	// +optional
	ReuseSysCVEAllowlist *bool `json:"reuseSysCVEAllowlist,omitempty"`

	// CVEAllowlist is the project CVE allowlist.
	// +optional
	CVEAllowlist *CVEAllowlist `json:"cveAllowlist,omitempty"`
}

// ProjectSpec defines the desired state of a Harbor project.
// +kubebuilder:validation:XValidation:rule="self.name == oldSelf.name",message="name is immutable"
type ProjectSpec struct {
	// HarborConnectionRef selects the Harbor instance.
	HarborConnectionRef HarborConnectionReference `json:"harborConnectionRef"`

	// Name of the project in Harbor. Immutable.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// Public makes the project readable by anyone.
	// +optional
	Public bool `json:"public,omitempty"`

	// StorageQuota is the storage limit of the project, e.g. "10Gi".
	// Unlimited when omitted.
	// +optional
	StorageQuota *resource.Quantity `json:"storageQuota,omitempty"`

	// Policies are the project configuration policies.
	// +optional
	Policies ProjectPolicies `json:"policies,omitempty"`

	// Members of the project. When non-empty, this list is authoritative:
	// members not listed are removed, except the HarborConnection user.
	// When empty, project membership is not managed.
	// +listType=map
	// +listMapKey=kind
	// +listMapKey=name
	// +optional
	Members []ProjectMember `json:"members,omitempty"`

	// DeletionPolicy controls whether the Harbor project is removed with this resource.
	// Harbor refuses to delete projects that still contain repositories.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ProjectStatus defines the observed state of Project.
type ProjectStatus struct {
	HarborResourceStatus `json:",inline"`

	// ProjectID is the Harbor project ID.
	// +optional
	ProjectID int64 `json:"projectID,omitempty"`

	// StorageUsed is the storage currently consumed by the project.
	// +optional
	StorageUsed *resource.Quantity `json:"storageUsed,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hp
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=`.status.projectID`
// +kubebuilder:printcolumn:name="Public",type=boolean,JSONPath=`.spec.public`
// +kubebuilder:printcolumn:name="Quota",type=string,JSONPath=`.spec.storageQuota`
// +kubebuilder:printcolumn:name="Used",type=string,JSONPath=`.status.storageUsed`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Project is a Harbor project with its members, quota and policies.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProjectSpec   `json:"spec,omitempty"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Project{}, &ProjectList{})
}
