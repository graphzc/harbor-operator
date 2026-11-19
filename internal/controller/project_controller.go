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
	"context"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

// ProjectReconciler reconciles a Project object
type ProjectReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=projects/finalizers,verbs=update

// Reconcile makes the Harbor project, its policies, quota and members match
// the Project spec.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var project harborv1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	api, apiErr := harborClient(ctx, r.Client, r.ClientFactory, project.Namespace, project.Spec.HarborConnectionRef)

	if !project.DeletionTimestamp.IsZero() {
		err := finalize(ctx, r.Client, &project, project.Spec.DeletionPolicy, api, apiErr, func(api harbor.API) error {
			if project.Status.ProjectID == 0 {
				return nil
			}
			log.Info("Deleting Harbor project", "id", project.Status.ProjectID)
			return ignoreHarborNotFound(api.DeleteProject(ctx, project.Status.ProjectID))
		})
		if err != nil {
			// Typically the project still holds repositories; surface it.
			setReady(&project.Status.HarborResourceStatus, project.Generation, metav1.ConditionFalse, reasonDeleteError, err.Error())
			_ = r.Status().Update(ctx, &project)
		}
		return ctrl.Result{}, err
	}
	if err := ensureFinalizer(ctx, r.Client, &project); err != nil {
		return ctrl.Result{}, err
	}

	err := apiErr
	if err == nil {
		err = r.sync(ctx, api, &project)
	}
	if err != nil {
		log.Error(err, "Failed to reconcile Harbor project")
		setReady(&project.Status.HarborResourceStatus, project.Generation, metav1.ConditionFalse, failureReason(err, reasonHarborError), err.Error())
	} else {
		setReady(&project.Status.HarborResourceStatus, project.Generation, metav1.ConditionTrue, reasonReconciled, "Project is in sync with Harbor")
	}
	if uerr := r.Status().Update(ctx, &project); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return resultFor(err)
}

func (r *ProjectReconciler) sync(ctx context.Context, api harbor.API, project *harborv1alpha1.Project) error {
	spec := project.Spec
	metadata := desiredProjectMetadata(spec)
	allowlist := desiredCVEAllowlist(spec.Policies.CVEAllowlist)

	existing, err := findProject(ctx, api, project.Status.ProjectID, spec.Name)
	if err != nil {
		return err
	}

	if existing == nil {
		storage := desiredStorage(spec.StorageQuota)
		id, err := api.CreateProject(ctx, harbor.ProjectReq{
			ProjectName:  spec.Name,
			Metadata:     metadata,
			CVEAllowlist: allowlist,
			StorageLimit: &storage,
		})
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Created Harbor project", "id", id)
		project.Status.ProjectID = id
	} else {
		project.Status.ProjectID = existing.ProjectID
		if metadataDiffers(existing.Metadata, metadata) || (allowlist != nil && !allowlistEqual(existing.CVEAllowlist, allowlist)) {
			if err := api.UpdateProject(ctx, existing.ProjectID, harbor.ProjectReq{
				Metadata:     metadata,
				CVEAllowlist: allowlist,
			}); err != nil {
				return err
			}
		}
	}

	if err := r.syncQuota(ctx, api, project); err != nil {
		return err
	}
	if len(spec.Members) > 0 {
		return r.syncMembers(ctx, api, project.Status.ProjectID, spec.Members)
	}
	return nil
}

func (r *ProjectReconciler) syncQuota(ctx context.Context, api harbor.API, project *harborv1alpha1.Project) error {
	quota, err := api.GetProjectQuota(ctx, project.Status.ProjectID)
	if err != nil {
		return err
	}
	want := desiredStorage(project.Spec.StorageQuota)
	if quota.Hard["storage"] != want {
		if err := api.UpdateQuota(ctx, quota.ID, want); err != nil {
			return err
		}
	}
	used := resource.NewQuantity(quota.Used["storage"], resource.BinarySI)
	project.Status.StorageUsed = used
	return nil
}

type memberKey struct {
	entityType string
	entityID   int64
}

func (r *ProjectReconciler) syncMembers(ctx context.Context, api harbor.API, projectID int64, members []harborv1alpha1.ProjectMember) error {
	current, err := api.ListMembers(ctx, projectID)
	if err != nil {
		return err
	}
	byEntity := map[memberKey]harbor.Member{}
	usersByName := map[string]harbor.Member{}
	for _, m := range current {
		byEntity[memberKey{m.EntityType, m.EntityID}] = m
		if m.EntityType == harbor.EntityTypeUser {
			usersByName[m.EntityName] = m
		}
	}

	keep := map[int64]bool{}
	for _, want := range members {
		roleID := harborRoleID(want.Role)
		var (
			existing harbor.Member
			found    bool
			req      = harbor.MemberReq{RoleID: roleID}
		)
		if want.Kind == harborv1alpha1.MemberKindGroup {
			g, err := api.FindGroup(ctx, want.Name, 0)
			if err != nil {
				return err
			}
			existing, found = byEntity[memberKey{harbor.EntityTypeGroup, g.ID}]
			req.MemberGroup = &harbor.MemberGroup{ID: g.ID}
		} else {
			existing, found = usersByName[want.Name]
			req.MemberUser = &harbor.MemberUser{Username: want.Name}
		}

		switch {
		case !found:
			if err := api.AddMember(ctx, projectID, req); err != nil {
				return err
			}
		case existing.RoleID != roleID:
			keep[existing.ID] = true
			if err := api.UpdateMemberRole(ctx, projectID, existing.ID, roleID); err != nil {
				return err
			}
		default:
			keep[existing.ID] = true
		}
	}

	for _, m := range current {
		if keep[m.ID] || (m.EntityType == harbor.EntityTypeUser && m.EntityName == api.Username()) {
			continue
		}
		if err := ignoreHarborNotFound(api.DeleteMember(ctx, projectID, m.ID)); err != nil {
			return err
		}
	}
	return nil
}

func findProject(ctx context.Context, api harbor.API, id int64, name string) (*harbor.Project, error) {
	if id != 0 {
		p, err := api.GetProject(ctx, id)
		if err == nil && p.Name == name {
			return p, nil
		}
		if err != nil && !harbor.IsNotFound(err) {
			return nil, err
		}
	}
	p, err := api.FindProject(ctx, name)
	if harbor.IsNotFound(err) {
		return nil, nil
	}
	return p, err
}

func desiredProjectMetadata(spec harborv1alpha1.ProjectSpec) map[string]string {
	md := map[string]string{"public": strconv.FormatBool(spec.Public)}
	p := spec.Policies
	setBool := func(key string, v *bool) {
		if v != nil {
			md[key] = strconv.FormatBool(*v)
		}
	}
	setBool("auto_scan", p.AutoScan)
	setBool("auto_sbom_generation", p.AutoSBOMGeneration)
	setBool("prevent_vul", p.PreventVulnerable)
	setBool("enable_content_trust", p.ContentTrust)
	setBool("enable_content_trust_cosign", p.ContentTrustCosign)
	setBool("reuse_sys_cve_allowlist", p.ReuseSysCVEAllowlist)
	if p.Severity != "" {
		md["severity"] = string(p.Severity)
	}
	return md
}

func metadataDiffers(current, desired map[string]string) bool {
	for k, v := range desired {
		if current[k] != v {
			return true
		}
	}
	return false
}

func desiredCVEAllowlist(a *harborv1alpha1.CVEAllowlist) *harbor.CVEAllowlist {
	if a == nil {
		return nil
	}
	out := &harbor.CVEAllowlist{Items: make([]harbor.CVEItem, 0, len(a.Items))}
	for _, id := range a.Items {
		out.Items = append(out.Items, harbor.CVEItem{CVEID: id})
	}
	if a.ExpiresAt != nil {
		ts := a.ExpiresAt.Unix()
		out.ExpiresAt = &ts
	}
	return out
}

func allowlistEqual(current, desired *harbor.CVEAllowlist) bool {
	if current == nil {
		return len(desired.Items) == 0 && desired.ExpiresAt == nil
	}
	ids := func(a *harbor.CVEAllowlist) []string {
		out := make([]string, 0, len(a.Items))
		for _, i := range a.Items {
			out = append(out, i.CVEID)
		}
		slices.Sort(out)
		return out
	}
	expiry := func(a *harbor.CVEAllowlist) int64 {
		if a.ExpiresAt == nil {
			return 0
		}
		return *a.ExpiresAt
	}
	return slices.Equal(ids(current), ids(desired)) && expiry(current) == expiry(desired)
}

// desiredStorage returns the storage limit in bytes; -1 means unlimited.
func desiredStorage(q *resource.Quantity) int64 {
	if q == nil {
		return -1
	}
	return q.Value()
}

func harborRoleID(role harborv1alpha1.ProjectRole) int {
	switch role {
	case harborv1alpha1.RoleProjectAdmin:
		return harbor.RoleProjectAdmin
	case harborv1alpha1.RoleMaintainer:
		return harbor.RoleMaintainer
	case harborv1alpha1.RoleDeveloper:
		return harbor.RoleDeveloper
	case harborv1alpha1.RoleLimitedGuest:
		return harbor.RoleLimitedGuest
	default:
		return harbor.RoleGuest
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupHarborResource(mgr, "project", &harborv1alpha1.Project{}, &harborv1alpha1.ProjectList{},
		func(o client.Object) string { return o.(*harborv1alpha1.Project).Spec.HarborConnectionRef.Name },
		func(client.Object) []string { return nil }, r)
}
