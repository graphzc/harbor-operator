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

package controller

import (
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

func TestGeneratePasswordMeetsHarborPolicy(t *testing.T) {
	for range 200 {
		p, err := generatePassword(generatedPasswordLength)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != generatedPasswordLength ||
			!strings.ContainsAny(p, lowerChars) || !strings.ContainsAny(p, upperChars) || !strings.ContainsAny(p, digitChars) {
			t.Fatalf("password %q does not meet Harbor's policy", p)
		}
	}
}

func TestSecretHash(t *testing.T) {
	h, err := hashSecret("S3cretValue")
	if err != nil {
		t.Fatal(err)
	}
	if !secretMatchesHash("S3cretValue", h) || secretMatchesHash("other", h) || secretMatchesHash("S3cretValue", "") {
		t.Fatal("hash comparison is wrong")
	}
}

func TestPermissionsEqualIgnoresOrderAndDefaultEffect(t *testing.T) {
	a := []harbor.RobotPermission{{Kind: "project", Namespace: "a", Access: []harbor.RobotAccess{
		{Resource: "repository", Action: "push"}, {Resource: "repository", Action: "pull", Effect: "allow"},
	}}}
	b := []harbor.RobotPermission{{Kind: "project", Namespace: "a", Access: []harbor.RobotAccess{
		{Resource: "repository", Action: "pull"}, {Resource: "repository", Action: "push", Effect: "allow"},
	}}}
	if !permissionsEqual(a, b) {
		t.Fatal("expected permissions to be equal")
	}
	b[0].Access[0].Action = "delete"
	if permissionsEqual(a, b) {
		t.Fatal("expected permissions to differ")
	}
}

func TestConfigValueEqual(t *testing.T) {
	for _, tc := range []struct {
		current, desired string
		equal            bool
	}{
		{`30`, `30`, true},
		{`30`, `"30"`, true},
		{`true`, `true`, true},
		{`true`, `"true"`, true},
		{`"adminonly"`, `"adminonly"`, true},
		{`""`, `null`, true},
		{`30`, `31`, false},
		{`false`, `true`, false},
		{`"everyone"`, `"adminonly"`, false},
	} {
		if got := configValueEqual([]byte(tc.current), []byte(tc.desired)); got != tc.equal {
			t.Errorf("configValueEqual(%s, %s) = %v, want %v", tc.current, tc.desired, got, tc.equal)
		}
	}
}

func TestSecretSettingsDigestDependsOnKeysAndValues(t *testing.T) {
	a := secretSettingsDigest(map[string]string{"oidc_client_secret": "x", "ldap_search_password": "y"})
	if a != secretSettingsDigest(map[string]string{"ldap_search_password": "y", "oidc_client_secret": "x"}) {
		t.Fatal("digest depends on map order")
	}
	if a == secretSettingsDigest(map[string]string{"oidc_client_secret": "y", "ldap_search_password": "x"}) {
		t.Fatal("digest ignores which key holds which value")
	}
}

func TestHarborConfigKey(t *testing.T) {
	for in, want := range map[string]string{
		"authMode":                   "auth_mode",
		"projectCreationRestriction": "project_creation_restriction",
		"oidcClientId":               "oidc_client_id",
		"oidcClientID":               "oidc_client_id",
		"ldapURL":                    "ldap_url",
		"ldapGroupAdminDn":           "ldap_group_admin_dn",
		"readOnly":                   "read_only",
	} {
		if got := harborConfigKey(in); got != want {
			t.Errorf("harborConfigKey(%q) = %q, want %q", in, got, want)
		}
	}
	if got := camelConfigKey("project_creation_restriction"); got != "projectCreationRestriction" {
		t.Errorf("camelConfigKey = %q", got)
	}
}

func TestHarborConfigKeysRejectsCollisions(t *testing.T) {
	spec := harborv1alpha1.ConfigurationSpec{Settings: map[string]apiextensionsv1.JSON{
		"ldapUrl": {Raw: []byte(`"a"`)}, "ldapURL": {Raw: []byte(`"b"`)},
	}}
	if _, err := harborConfigKeys(spec); err == nil || !strings.Contains(err.Error(), "both set Harbor configuration ldap_url") {
		t.Fatalf("expected a collision error, got %v", err)
	}
}
