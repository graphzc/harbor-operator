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
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

func key(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: testNamespace, Name: name}
}

func connRef() harborv1alpha1.HarborConnectionReference {
	return harborv1alpha1.HarborConnectionReference{Name: testConnection}
}

func createSecret(name string, data map[string]string) {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name}, StringData: data}
	Expect(k8sClient.Create(ctx, s)).To(Succeed())
}

func updateSecret(name, k, v string) {
	var s corev1.Secret
	Expect(k8sClient.Get(ctx, key(name), &s)).To(Succeed())
	s.Data[k] = []byte(v)
	Expect(k8sClient.Update(ctx, &s)).To(Succeed())
}

// expectReady waits for obj to have Ready=True for its current generation.
func expectReady(obj client.Object, conditions func() []metav1.Condition) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
		cond := meta.FindStatusCondition(conditions(), harborv1alpha1.ConditionReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), cond.Message)
		g.Expect(cond.ObservedGeneration).To(Equal(obj.GetGeneration()))
	}, timeout, interval).Should(Succeed())
}

func deleteAndWait(obj client.Object) {
	GinkgoHelper()
	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	}, timeout, interval).Should(BeTrue())
}

// withHarbor runs fn while holding the fake Harbor lock.
func withHarbor[T any](fn func() T) func() T {
	return func() T {
		fakeHarbor.Mu.Lock()
		defer fakeHarbor.Mu.Unlock()
		return fn()
	}
}

var _ = Describe("Harbor controllers", Ordered, func() {
	BeforeAll(func() {
		createSecret("harbor-admin", map[string]string{"password": fakeAdminPassword})
		conn := &harborv1alpha1.HarborConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testConnection},
			Spec: harborv1alpha1.HarborConnectionSpec{
				URL:               fakeHarbor.URL,
				PasswordSecretRef: harborv1alpha1.SecretKeySelector{Name: "harbor-admin"},
			},
		}
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
	})

	It("connects to Harbor", func() {
		conn := &harborv1alpha1.HarborConnection{}
		conn.Namespace, conn.Name = testNamespace, testConnection
		expectReady(conn, func() []metav1.Condition { return conn.Status.Conditions })
		Expect(conn.Status.HarborVersion).To(Equal("v2.13.0-fake"))
		Expect(conn.Status.AuthMode).To(Equal("db_auth"))
	})

	It("reports a bad admin password", func() {
		createSecret("bad-admin", map[string]string{"password": "wrong"})
		conn := &harborv1alpha1.HarborConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "bad"},
			Spec: harborv1alpha1.HarborConnectionSpec{
				URL:               fakeHarbor.URL,
				PasswordSecretRef: harborv1alpha1.SecretKeySelector{Name: "bad-admin"},
			},
		}
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key("bad"), conn)).To(Succeed())
			cond := meta.FindStatusCondition(conn.Status.Conditions, harborv1alpha1.ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(ContainSubstring("401"))
		}, timeout, interval).Should(Succeed())

		By("fixing the password Secret")
		updateSecret("bad-admin", "password", fakeAdminPassword)
		expectReady(conn, func() []metav1.Condition { return conn.Status.Conditions })
	})

	It("manages a user with a generated password", func() {
		user := &harborv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "alice"},
			Spec: harborv1alpha1.UserSpec{
				HarborConnectionRef: connRef(),
				Username:            "alice",
				Email:               "alice@example.com",
			},
		}
		Expect(k8sClient.Create(ctx, user)).To(Succeed())
		expectReady(user, func() []metav1.Condition { return user.Status.Conditions })
		Expect(user.Status.SecretName).To(Equal("alice-harbor-credentials"))

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key("alice-harbor-credentials"), &secret)).To(Succeed())
		Expect(string(secret.Data["username"])).To(Equal("alice"))
		Expect(secret.Data["password"]).To(HaveLen(generatedPasswordLength))
		Expect(metav1.IsControlledBy(&secret, user)).To(BeTrue())

		Expect(withHarbor(func() string { return fakeHarbor.Passwords[user.Status.UserID] })()).
			To(Equal(string(secret.Data["password"])))

		By("updating the profile and granting sysadmin")
		user.Spec.Email = "alice@corp.example.com"
		user.Spec.SysAdmin = true
		Expect(k8sClient.Update(ctx, user)).To(Succeed())
		Eventually(withHarbor(func() harbor.User { return *fakeHarbor.Users[user.Status.UserID] }), timeout, interval).
			Should(And(
				HaveField("Email", "alice@corp.example.com"),
				HaveField("SysAdminFlag", true),
			))

		By("deleting the user")
		id := user.Status.UserID
		deleteAndWait(user)
		Expect(withHarbor(func() bool { _, ok := fakeHarbor.Users[id]; return ok })()).To(BeFalse())
	})

	It("applies a referenced password and its changes", func() {
		createSecret("bob-password", map[string]string{"password": "BobPassw0rd"})
		user := &harborv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "bob"},
			Spec: harborv1alpha1.UserSpec{
				HarborConnectionRef: connRef(),
				Username:            "bob",
				Email:               "bob@example.com",
				PasswordSecretRef:   &harborv1alpha1.SecretKeySelector{Name: "bob-password"},
				DeletionPolicy:      harborv1alpha1.DeletionPolicyOrphan,
			},
		}
		Expect(k8sClient.Create(ctx, user)).To(Succeed())
		expectReady(user, func() []metav1.Condition { return user.Status.Conditions })
		id := user.Status.UserID
		Expect(withHarbor(func() string { return fakeHarbor.Passwords[id] })()).To(Equal("BobPassw0rd"))

		By("rotating the password Secret")
		updateSecret("bob-password", "password", "BobNewPassw0rd")
		Eventually(withHarbor(func() string { return fakeHarbor.Passwords[id] }), timeout, interval).
			Should(Equal("BobNewPassw0rd"))

		By("orphaning the user on delete")
		deleteAndWait(user)
		Expect(withHarbor(func() bool { _, ok := fakeHarbor.Users[id]; return ok })()).To(BeTrue())
	})

	It("refuses to manage the connection user", func() {
		user := &harborv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "admin"},
			Spec:       harborv1alpha1.UserSpec{HarborConnectionRef: connRef(), Username: "admin", Email: "admin@example.com"},
		}
		Expect(k8sClient.Create(ctx, user)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key("admin"), user)).To(Succeed())
			cond := meta.FindStatusCondition(user.Status.Conditions, harborv1alpha1.ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		}, timeout, interval).Should(Succeed())
		Expect(withHarbor(func() string { return fakeHarbor.Passwords[1] })()).To(Equal(fakeAdminPassword))
		deleteAndWait(user)
	})

	It("manages a group", func() {
		group := &harborv1alpha1.Group{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "devs"},
			Spec:       harborv1alpha1.GroupSpec{HarborConnectionRef: connRef(), GroupName: "devs"},
		}
		Expect(k8sClient.Create(ctx, group)).To(Succeed())
		expectReady(group, func() []metav1.Condition { return group.Status.Conditions })
		id := group.Status.GroupID
		Expect(withHarbor(func() harbor.Group { return *fakeHarbor.Groups[id] })()).
			To(And(HaveField("GroupName", "devs"), HaveField("GroupType", harbor.GroupTypeHTTP)))

		By("keeping the group ID across reconciles")
		group.Spec.DeletionPolicy = harborv1alpha1.DeletionPolicyOrphan
		Expect(k8sClient.Update(ctx, group)).To(Succeed())
		expectReady(group, func() []metav1.Condition { return group.Status.Conditions })
		Expect(group.Status.GroupID).To(Equal(id))
	})

	It("manages a project with quota, policies and members", func() {
		createSecret("carol-password", map[string]string{"password": "CarolPassw0rd"})
		carol := &harborv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "carol"},
			Spec: harborv1alpha1.UserSpec{
				HarborConnectionRef: connRef(), Username: "carol", Email: "carol@example.com",
				PasswordSecretRef: &harborv1alpha1.SecretKeySelector{Name: "carol-password"},
			},
		}
		Expect(k8sClient.Create(ctx, carol)).To(Succeed())
		expectReady(carol, func() []metav1.Condition { return carol.Status.Conditions })

		quota := resource.MustParse("10Gi")
		project := &harborv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "team-a"},
			Spec: harborv1alpha1.ProjectSpec{
				HarborConnectionRef: connRef(),
				Name:                "team-a",
				StorageQuota:        &quota,
				Policies: harborv1alpha1.ProjectPolicies{
					AutoScan:          ptr.To(true),
					PreventVulnerable: ptr.To(true),
					Severity:          "high",
					CVEAllowlist:      &harborv1alpha1.CVEAllowlist{Items: []string{"CVE-2024-0001"}},
				},
				Members: []harborv1alpha1.ProjectMember{
					{Kind: harborv1alpha1.MemberKindUser, Name: "carol", Role: harborv1alpha1.RoleDeveloper},
					{Kind: harborv1alpha1.MemberKindGroup, Name: "devs", Role: harborv1alpha1.RoleGuest},
				},
			},
		}
		Expect(k8sClient.Create(ctx, project)).To(Succeed())
		expectReady(project, func() []metav1.Condition { return project.Status.Conditions })
		pid := project.Status.ProjectID

		p := withHarbor(func() harbor.Project { return *fakeHarbor.Projects[pid] })()
		Expect(p.Metadata).To(HaveKeyWithValue("auto_scan", "true"))
		Expect(p.Metadata).To(HaveKeyWithValue("prevent_vul", "true"))
		Expect(p.Metadata).To(HaveKeyWithValue("severity", "high"))
		Expect(p.Metadata).To(HaveKeyWithValue("public", "false"))
		Expect(p.CVEAllowlist.Items).To(ConsistOf(harbor.CVEItem{CVEID: "CVE-2024-0001"}))
		Expect(withHarbor(func() int64 { return fakeHarbor.Quotas[pid].Hard["storage"] })()).To(Equal(quota.Value()))

		roles := func() map[string]int {
			out := map[string]int{}
			for _, m := range fakeHarbor.Members[pid] {
				out[m.EntityType+":"+m.EntityName] = m.RoleID
			}
			return out
		}
		Expect(withHarbor(roles)()).To(Equal(map[string]int{
			"u:admin": harbor.RoleProjectAdmin,
			"u:carol": harbor.RoleDeveloper,
			"g:devs":  harbor.RoleGuest,
		}))

		By("changing visibility, quota and members")
		Expect(k8sClient.Get(ctx, key("team-a"), project)).To(Succeed())
		project.Spec.Public = true
		project.Spec.StorageQuota = nil
		project.Spec.Members = []harborv1alpha1.ProjectMember{
			{Kind: harborv1alpha1.MemberKindUser, Name: "carol", Role: harborv1alpha1.RoleMaintainer},
		}
		Expect(k8sClient.Update(ctx, project)).To(Succeed())
		Eventually(withHarbor(roles), timeout, interval).Should(Equal(map[string]int{
			"u:admin": harbor.RoleProjectAdmin,
			"u:carol": harbor.RoleMaintainer,
		}))
		Eventually(withHarbor(func() string { return fakeHarbor.Projects[pid].Metadata["public"] }), timeout, interval).
			Should(Equal("true"))
		Eventually(withHarbor(func() int64 { return fakeHarbor.Quotas[pid].Hard["storage"] }), timeout, interval).
			Should(Equal(int64(-1)))
	})

	It("manages a system robot with a generated secret", func() {
		robot := &harborv1alpha1.RobotAccount{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "ci"},
			Spec: harborv1alpha1.RobotAccountSpec{
				HarborConnectionRef: connRef(),
				Name:                "ci",
				Level:               harborv1alpha1.RobotLevelSystem,
				Duration:            -1,
				Permissions: []harborv1alpha1.RobotPermission{{
					Kind: "project", Namespace: "*",
					Access: []harborv1alpha1.RobotAccess{{Resource: "repository", Action: "pull"}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		expectReady(robot, func() []metav1.Condition { return robot.Status.Conditions })
		Expect(robot.Status.FullName).To(Equal("robot$ci"))

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key("ci-harbor-credentials"), &secret)).To(Succeed())
		Expect(string(secret.Data["username"])).To(Equal("robot$ci"))
		id := robot.Status.RobotID
		Expect(withHarbor(func() string { return fakeHarbor.RobotSecrets[id] })()).To(Equal(string(secret.Data["password"])))

		By("updating permissions")
		robot.Spec.Permissions[0].Access = append(robot.Spec.Permissions[0].Access,
			harborv1alpha1.RobotAccess{Resource: "repository", Action: "push"})
		Expect(k8sClient.Update(ctx, robot)).To(Succeed())
		Eventually(withHarbor(func() int { return len(fakeHarbor.Robots[id].Permissions[0].Access) }), timeout, interval).
			Should(Equal(2))

		By("restoring the generated Secret when it is deleted")
		uid := secret.UID
		Expect(k8sClient.Delete(ctx, &secret)).To(Succeed())
		Eventually(func(g Gomega) {
			var s corev1.Secret
			g.Expect(k8sClient.Get(ctx, key("ci-harbor-credentials"), &s)).To(Succeed())
			g.Expect(s.UID).NotTo(Equal(uid))
			g.Expect(withHarbor(func() string { return fakeHarbor.RobotSecrets[id] })()).To(Equal(string(s.Data["password"])))
		}, timeout, interval).Should(Succeed())

		deleteAndWait(robot)
		Expect(withHarbor(func() bool { _, ok := fakeHarbor.Robots[id]; return ok })()).To(BeFalse())
	})

	It("manages a project robot with a referenced secret", func() {
		createSecret("deployer-secret", map[string]string{"token": "DeployerS3cret"})
		robot := &harborv1alpha1.RobotAccount{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "deployer"},
			Spec: harborv1alpha1.RobotAccountSpec{
				HarborConnectionRef: connRef(),
				Name:                "deployer",
				Level:               harborv1alpha1.RobotLevelProject,
				Project:             "team-a",
				Duration:            30,
				Permissions: []harborv1alpha1.RobotPermission{{
					Access: []harborv1alpha1.RobotAccess{{Resource: "repository", Action: "push"}},
				}},
				SecretRef: &harborv1alpha1.SecretKeySelector{Name: "deployer-secret", Key: "token"},
			},
		}
		Expect(k8sClient.Create(ctx, robot)).To(Succeed())
		expectReady(robot, func() []metav1.Condition { return robot.Status.Conditions })
		Expect(robot.Status.FullName).To(Equal("robot$team-a+deployer"))
		id := robot.Status.RobotID
		Expect(withHarbor(func() harbor.RobotPermission { return fakeHarbor.Robots[id].Permissions[0] })()).
			To(And(HaveField("Kind", "project"), HaveField("Namespace", "team-a")))
		Expect(withHarbor(func() string { return fakeHarbor.RobotSecrets[id] })()).To(Equal("DeployerS3cret"))

		By("rotating the referenced secret")
		updateSecret("deployer-secret", "token", "DeployerN3wSecret")
		Eventually(withHarbor(func() string { return fakeHarbor.RobotSecrets[id] }), timeout, interval).
			Should(Equal("DeployerN3wSecret"))
		// Wait for the status write of that reconcile so robot is not stale.
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key("deployer"), robot)).To(Succeed())
			g.Expect(secretMatchesHash("DeployerN3wSecret", robot.Status.SecretHash)).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		By("adopting the existing project robot when status is lost")
		robot.Status = harborv1alpha1.RobotAccountStatus{}
		Expect(k8sClient.Status().Update(ctx, robot)).To(Succeed())
		robot.Spec.Description = "adopted"
		Expect(k8sClient.Update(ctx, robot)).To(Succeed())
		expectReady(robot, func() []metav1.Condition { return robot.Status.Conditions })
		Expect(robot.Status.RobotID).To(Equal(id))
		Expect(withHarbor(func() int { return len(fakeHarbor.Robots) })()).To(Equal(1))
	})

	It("reconciles when the stored password hash is lost", func() {
		createSecret("dave-password", map[string]string{"password": "DavePassw0rd"})
		user := &harborv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "dave"},
			Spec: harborv1alpha1.UserSpec{
				HarborConnectionRef: connRef(), Username: "dave", Email: "dave@example.com",
				PasswordSecretRef: &harborv1alpha1.SecretKeySelector{Name: "dave-password"},
			},
		}
		Expect(k8sClient.Create(ctx, user)).To(Succeed())
		expectReady(user, func() []metav1.Condition { return user.Status.Conditions })

		By("clearing status and forcing a reconcile")
		user.Status = harborv1alpha1.UserStatus{}
		Expect(k8sClient.Status().Update(ctx, user)).To(Succeed())
		user.Spec.Comment = "touched"
		Expect(k8sClient.Update(ctx, user)).To(Succeed())
		expectReady(user, func() []metav1.Condition { return user.Status.Conditions })
		Expect(user.Status.PasswordHash).NotTo(BeEmpty())
		deleteAndWait(user)
	})

	It("manages the Harbor configuration", func() {
		createSecret("oidc", map[string]string{"clientSecret": "OidcS3cret"})
		config := &harborv1alpha1.Configuration{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "harbor"},
			Spec: harborv1alpha1.ConfigurationSpec{
				HarborConnectionRef: connRef(),
				Settings: map[string]apiextensionsv1.JSON{
					"projectCreationRestriction": {Raw: []byte(`"adminonly"`)},
					"robotTokenDuration":         {Raw: []byte(`90`)},
					"selfRegistration":           {Raw: []byte(`false`)},
				},
				SecretSettings: map[string]harborv1alpha1.SecretKeySelector{
					"oidcClientSecret": {Name: "oidc", Key: "clientSecret"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, config)).To(Succeed())
		expectReady(config, func() []metav1.Condition { return config.Status.Conditions })
		Expect(config.Status.SecretSettingsHash).NotTo(BeEmpty())
		settings := func() map[string]any { return maps.Clone(fakeHarbor.Configurations) }
		Expect(withHarbor(settings)()).To(And(
			HaveKeyWithValue("project_creation_restriction", "adminonly"),
			HaveKeyWithValue("robot_token_duration", float64(90)),
			HaveKeyWithValue("oidc_client_secret", "OidcS3cret"),
			HaveKeyWithValue("auth_mode", "db_auth"),
		))
		updates := withHarbor(func() int { return fakeHarbor.ConfigUpdates })

		// The controller writes status on every attempt, and retries failures
		// with backoff, so spec changes are retried on conflict.
		updateConfig := func(mutate func(spec *harborv1alpha1.ConfigurationSpec)) error {
			var err error
			Eventually(func() bool {
				if err = k8sClient.Get(ctx, key("harbor"), config); err != nil {
					return false
				}
				mutate(&config.Spec)
				err = k8sClient.Update(ctx, config)
				return !apierrors.IsConflict(err)
			}, timeout, interval).Should(BeTrue())
			return err
		}
		expectNotReady := func(message string) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key("harbor"), config)).To(Succeed())
				cond := meta.FindStatusCondition(config.Status.Conditions, harborv1alpha1.ConditionReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.ObservedGeneration).To(Equal(config.Generation))
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring(message))
			}, timeout, interval).Should(Succeed())
		}

		By("not updating Harbor when it is in sync")
		before := updates()
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			spec.Settings["robotTokenDuration"] = apiextensionsv1.JSON{Raw: []byte(`"90"`)}
		})).To(Succeed())
		expectReady(config, func() []metav1.Condition { return config.Status.Conditions })
		Expect(updates()).To(Equal(before))

		By("reverting drift and applying a rotated secret")
		withHarbor(func() any { fakeHarbor.Configurations["project_creation_restriction"] = "everyone"; return nil })()
		updateSecret("oidc", "clientSecret", "OidcN3wSecret")
		Eventually(withHarbor(settings), timeout, interval).Should(And(
			HaveKeyWithValue("project_creation_restriction", "adminonly"),
			HaveKeyWithValue("oidc_client_secret", "OidcN3wSecret"),
		))

		By("reporting a key Harbor does not know")
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			spec.Settings["noSuchSetting"] = apiextensionsv1.JSON{Raw: []byte(`true`)}
		})).To(Succeed())
		expectNotReady("no_such_setting")
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			delete(spec.Settings, "noSuchSetting")
		})).To(Succeed())
		expectReady(config, func() []metav1.Condition { return config.Status.Conditions })

		By("reporting a snake_case key")
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			spec.Settings["read_only"] = apiextensionsv1.JSON{Raw: []byte(`true`)}
		})).To(Succeed())
		expectNotReady(`settings key "read_only" must be camelCase, e.g. "readOnly"`)
		Expect(withHarbor(settings)()).To(HaveKeyWithValue("read_only", false))

		By("reporting a key in both settings and secretSettings")
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			delete(spec.Settings, "read_only")
			spec.Settings["oidcClientSecret"] = apiextensionsv1.JSON{Raw: []byte(`"plain"`)}
		})).To(Succeed())
		expectNotReady("settings.oidcClientSecret and secretSettings.oidcClientSecret both set Harbor configuration oidc_client_secret")
		Expect(withHarbor(settings)()).To(HaveKeyWithValue("oidc_client_secret", "OidcN3wSecret"))

		By("rejecting a snake_case secretSettings key at admission")
		Expect(updateConfig(func(spec *harborv1alpha1.ConfigurationSpec) {
			spec.SecretSettings["ldap_search_password"] = harborv1alpha1.SecretKeySelector{Name: "oidc"}
		})).To(MatchError(ContainSubstring("keys must be camelCase")))

		By("leaving the settings in Harbor on delete")
		deleteAndWait(config)
		Expect(withHarbor(settings)()).To(HaveKeyWithValue("project_creation_restriction", "adminonly"))
	})

	It("deletes the project", func() {
		project := &harborv1alpha1.Project{}
		Expect(k8sClient.Get(ctx, key("team-a"), project)).To(Succeed())
		pid := project.Status.ProjectID
		deleteAndWait(project)
		Expect(withHarbor(func() bool { _, ok := fakeHarbor.Projects[pid]; return ok })()).To(BeFalse())
	})
})
