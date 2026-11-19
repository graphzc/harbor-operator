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
