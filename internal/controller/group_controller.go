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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

// GroupReconciler reconciles a Group object
type GroupReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=groups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=groups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=groups/finalizers,verbs=update

// Reconcile makes the Harbor user group match the Group spec.
func (r *GroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var group harborv1alpha1.Group
	if err := r.Get(ctx, req.NamespacedName, &group); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	api, apiErr := harborClient(ctx, r.Client, r.ClientFactory, group.Namespace, group.Spec.HarborConnectionRef)

	if !group.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, finalize(ctx, r.Client, &group, group.Spec.DeletionPolicy, api, apiErr, func(api harbor.API) error {
			if group.Status.GroupID == 0 {
				return nil
			}
			log.Info("Deleting Harbor group", "id", group.Status.GroupID)
			return ignoreHarborNotFound(api.DeleteGroup(ctx, group.Status.GroupID))
		})
	}
	if err := ensureFinalizer(ctx, r.Client, &group); err != nil {
		return ctrl.Result{}, err
	}

	err := apiErr
	if err == nil {
		err = r.sync(ctx, api, &group)
	}
	if err != nil {
		log.Error(err, "Failed to reconcile Harbor group")
		setReady(&group.Status.HarborResourceStatus, group.Generation, metav1.ConditionFalse, failureReason(err, reasonHarborError), err.Error())
	} else {
		setReady(&group.Status.HarborResourceStatus, group.Generation, metav1.ConditionTrue, reasonReconciled, "Group is in sync with Harbor")
	}
	if uerr := r.Status().Update(ctx, &group); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return resultFor(err)
}

func (r *GroupReconciler) sync(ctx context.Context, api harbor.API, group *harborv1alpha1.Group) error {
	spec := group.Spec
	groupType := harborGroupType(spec.GroupType)

	var existing *harbor.Group
	if id := group.Status.GroupID; id != 0 {
		g, err := api.GetGroup(ctx, id)
		switch {
		case err == nil:
			existing = g
		case !harbor.IsNotFound(err):
			return err
		}
	}
	if existing == nil {
		g, err := api.FindGroup(ctx, spec.GroupName, groupType)
		switch {
		case err == nil:
			existing = g
		case !harbor.IsNotFound(err):
			return err
		}
	}

	if existing == nil {
		id, err := api.CreateGroup(ctx, harbor.Group{GroupName: spec.GroupName, GroupType: groupType, LDAPGroupDN: spec.LDAPGroupDN})
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Created Harbor group", "id", id)
		group.Status.GroupID = id
		return nil
	}

	group.Status.GroupID = existing.ID
	if existing.GroupName != spec.GroupName {
		return api.UpdateGroup(ctx, existing.ID, spec.GroupName)
	}
	return nil
}

func harborGroupType(t harborv1alpha1.GroupType) int {
	switch t {
	case harborv1alpha1.GroupTypeLDAP:
		return harbor.GroupTypeLDAP
	case harborv1alpha1.GroupTypeOIDC:
		return harbor.GroupTypeOIDC
	default:
		return harbor.GroupTypeHTTP
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupHarborResource(mgr, "group", &harborv1alpha1.Group{}, &harborv1alpha1.GroupList{},
		func(o client.Object) string { return o.(*harborv1alpha1.Group).Spec.HarborConnectionRef.Name },
		func(client.Object) []string { return nil }, r)
}
