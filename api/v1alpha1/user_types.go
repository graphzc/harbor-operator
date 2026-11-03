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

// UserSpec defines the desired state of a Harbor user account.
// +kubebuilder:validation:XValidation:rule="self.username == oldSelf.username",message="username is immutable"
type UserSpec struct {
	// HarborConnectionRef selects the Harbor instance.
	HarborConnectionRef HarborConnectionReference `json:"harborConnectionRef"`

	// Username in Harbor. Immutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Username string `json:"username"`

	// Email address of the user.
	// +kubebuilder:validation:MinLength=1
	Email string `json:"email"`

	// RealName of the user. Defaults to the username.
	// +optional
	RealName string `json:"realName,omitempty"`

	// Comment attached to the user.
	// +optional
	Comment string `json:"comment,omitempty"`

	// SysAdmin grants Harbor system administrator privileges.
	// +optional
	SysAdmin bool `json:"sysAdmin,omitempty"`

	// PasswordSecretRef selects an existing Secret key holding the password.
	// When omitted, a password is generated and stored in GeneratedSecret.
	// +optional
	PasswordSecretRef *SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// GeneratedSecret configures the Secret created for a generated password.
	// The Secret holds the keys "username" and "password".
	// +optional
	GeneratedSecret GeneratedSecret `json:"generatedSecret,omitempty"`

	// DeletionPolicy controls whether the Harbor user is removed with this resource.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// UserStatus defines the observed state of User.
type UserStatus struct {
	HarborResourceStatus `json:",inline"`

	// UserID is the Harbor user ID.
	// +optional
	UserID int64 `json:"userID,omitempty"`

	// SecretName is the Secret holding the user's credentials.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// PasswordHash is a hash of the last password applied to Harbor, used to
	// detect password changes without storing the password.
	// +optional
	PasswordHash string `json:"passwordHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hu
// +kubebuilder:printcolumn:name="Username",type=string,JSONPath=`.spec.username`
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=`.status.userID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// User is a Harbor user account (requires Harbor database authentication).
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec,omitempty"`
	Status UserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User.
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(&User{}, &UserList{})
}
