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
	"cmp"
	"context"
	"reflect"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

// RobotAccountReconciler reconciles a RobotAccount object
type RobotAccountReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=robotaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=robotaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=robotaccounts/finalizers,verbs=update

// Reconcile makes the Harbor robot account and its secret match the spec.
func (r *RobotAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var robot harborv1alpha1.RobotAccount
	if err := r.Get(ctx, req.NamespacedName, &robot); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	api, apiErr := harborClient(ctx, r.Client, r.ClientFactory, robot.Namespace, robot.Spec.HarborConnectionRef)

	if !robot.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, finalize(ctx, r.Client, &robot, robot.Spec.DeletionPolicy, api, apiErr, func(api harbor.API) error {
			if robot.Status.RobotID == 0 {
				return nil
			}
			log.Info("Deleting Harbor robot", "id", robot.Status.RobotID)
			return ignoreHarborNotFound(api.DeleteRobot(ctx, robot.Status.RobotID))
		})
	}
	if err := ensureFinalizer(ctx, r.Client, &robot); err != nil {
		return ctrl.Result{}, err
	}

	err := apiErr
	if err == nil {
		err = r.sync(ctx, api, &robot)
	}
	if err != nil {
		log.Error(err, "Failed to reconcile Harbor robot account")
		setReady(&robot.Status.HarborResourceStatus, robot.Generation, metav1.ConditionFalse, failureReason(err, reasonHarborError), err.Error())
	} else {
		setReady(&robot.Status.HarborResourceStatus, robot.Generation, metav1.ConditionTrue, reasonReconciled, "Robot account is in sync with Harbor")
	}
	if uerr := r.Status().Update(ctx, &robot); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return resultFor(err)
}

func (r *RobotAccountReconciler) sync(ctx context.Context, api harbor.API, robot *harborv1alpha1.RobotAccount) error {
	spec := robot.Spec
	level := string(cmp.Or(spec.Level, harborv1alpha1.RobotLevelSystem))
	perms := desiredRobotPermissions(spec)

	// Resolve a referenced secret up front so a missing Secret blocks creation.
	var refSecret string
	if spec.SecretRef != nil {
		s, err := readSecretKey(ctx, r.Client, robot.Namespace, *spec.SecretRef)
		if err != nil {
			return err
		}
		refSecret = s
		robot.Status.SecretName = spec.SecretRef.Name
	}

	existing, err := r.find(ctx, api, robot)
	if err != nil {
		return err
	}

	if existing == nil {
		created, err := api.CreateRobot(ctx, harbor.RobotCreate{
			Name:        spec.Name,
			Description: spec.Description,
			Level:       level,
			Duration:    spec.Duration,
			Disable:     spec.Disabled,
			Permissions: perms,
		})
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Created Harbor robot", "id", created.ID, "name", created.Name)
		robot.Status.RobotID = created.ID
		robot.Status.FullName = created.Name
		robot.Status.ExpiresAt = expiryTime(created.ExpiresAt)

		secret := created.Secret
		if refSecret != "" {
			if secret, err = api.SetRobotSecret(ctx, created.ID, refSecret); err != nil {
				return err
			}
		} else if err := r.storeGenerated(ctx, robot, secret); err != nil {
			return err
		}
		robot.Status.SecretHash, err = hashSecret(secret)
		return err
	}

	robot.Status.RobotID = existing.ID
	robot.Status.FullName = existing.Name
	robot.Status.ExpiresAt = expiryTime(existing.ExpiresAt)

	if existing.Description != spec.Description || existing.Duration != spec.Duration ||
		existing.Disable != spec.Disabled || !permissionsEqual(existing.Permissions, perms) {
		if err := api.UpdateRobot(ctx, existing.ID, harbor.Robot{
			Name:        existing.Name,
			Level:       existing.Level,
			Description: spec.Description,
			Duration:    spec.Duration,
			Disable:     spec.Disabled,
			Permissions: perms,
		}); err != nil {
			return err
		}
		// Changing the duration moves the expiry; refresh it.
		if updated, err := api.GetRobot(ctx, existing.ID); err == nil {
			robot.Status.ExpiresAt = expiryTime(updated.ExpiresAt)
		}
	}

	return r.syncSecret(ctx, api, robot, existing.ID, refSecret)
}

// syncSecret makes the Harbor robot secret match the referenced Secret, or
// keeps the generated Secret valid.
func (r *RobotAccountReconciler) syncSecret(ctx context.Context, api harbor.API, robot *harborv1alpha1.RobotAccount, id int64, refSecret string) error {
	want := refSecret
	if want == "" {
		name := generatedSecretName(robot, robot.Spec.GeneratedSecret)
		stored, err := getGeneratedPassword(ctx, r.Client, robot.Namespace, name)
		if err != nil {
			return err
		}
		want = stored
	}

	if want != "" && secretMatchesHash(want, robot.Status.SecretHash) {
		if refSecret == "" {
			// Keep the username key in sync with the robot name.
			return r.storeGenerated(ctx, robot, want)
		}
		return nil
	}

	// An empty desired secret asks Harbor to generate a new one.
	secret, err := api.SetRobotSecret(ctx, id, want)
	if err != nil {
		return err
	}
	if refSecret == "" {
		if err := r.storeGenerated(ctx, robot, secret); err != nil {
			return err
		}
	}
	robot.Status.SecretHash, err = hashSecret(secret)
	return err
}

func (r *RobotAccountReconciler) storeGenerated(ctx context.Context, robot *harborv1alpha1.RobotAccount, secret string) error {
	name := generatedSecretName(robot, robot.Spec.GeneratedSecret)
	if err := writeGeneratedSecret(ctx, r.Client, robot, name, robot.Status.FullName, secret); err != nil {
		return err
	}
	robot.Status.SecretName = name
	return nil
}

func (r *RobotAccountReconciler) find(ctx context.Context, api harbor.API, robot *harborv1alpha1.RobotAccount) (*harbor.Robot, error) {
	name := robotHarborName(robot.Spec)
	if id := robot.Status.RobotID; id != 0 {
		rb, err := api.GetRobot(ctx, id)
		if err == nil && harbor.RobotNameMatches(rb.Name, name) {
			return rb, nil
		}
		if err != nil && !harbor.IsNotFound(err) {
			return nil, err
		}
	}
	var projectID int64
	if robot.Spec.Level == harborv1alpha1.RobotLevelProject {
		p, err := api.FindProject(ctx, robot.Spec.Project)
		if err != nil {
			return nil, err
		}
		projectID = p.ProjectID
	}
	rb, err := api.FindRobot(ctx, name, projectID)
	if harbor.IsNotFound(err) {
		return nil, nil
	}
	return rb, err
}

// robotHarborName is the robot name without the configurable prefix.
func robotHarborName(spec harborv1alpha1.RobotAccountSpec) string {
	if spec.Level == harborv1alpha1.RobotLevelProject {
		return spec.Project + "+" + spec.Name
	}
	return spec.Name
}

func desiredRobotPermissions(spec harborv1alpha1.RobotAccountSpec) []harbor.RobotPermission {
	out := make([]harbor.RobotPermission, 0, len(spec.Permissions))
	for _, p := range spec.Permissions {
		hp := harbor.RobotPermission{Kind: cmp.Or(p.Kind, "project"), Namespace: p.Namespace}
		if spec.Level == harborv1alpha1.RobotLevelProject {
			hp.Kind = "project"
			hp.Namespace = spec.Project
		}
		for _, a := range p.Access {
			hp.Access = append(hp.Access, harbor.RobotAccess{Resource: a.Resource, Action: a.Action, Effect: a.Effect})
		}
		out = append(out, hp)
	}
	return out
}

// permissionsEqual compares permissions ignoring order and default effects.
func permissionsEqual(a, b []harbor.RobotPermission) bool {
	return reflect.DeepEqual(normalizePermissions(a), normalizePermissions(b))
}

func normalizePermissions(perms []harbor.RobotPermission) []harbor.RobotPermission {
	out := make([]harbor.RobotPermission, 0, len(perms))
	for _, p := range perms {
		access := make([]harbor.RobotAccess, 0, len(p.Access))
		for _, a := range p.Access {
			a.Effect = cmp.Or(a.Effect, "allow")
			access = append(access, a)
		}
		slices.SortFunc(access, func(x, y harbor.RobotAccess) int {
			return cmp.Or(cmp.Compare(x.Resource, y.Resource), cmp.Compare(x.Action, y.Action), cmp.Compare(x.Effect, y.Effect))
		})
		out = append(out, harbor.RobotPermission{Kind: p.Kind, Namespace: p.Namespace, Access: access})
	}
	slices.SortFunc(out, func(x, y harbor.RobotPermission) int {
		return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.Namespace, y.Namespace))
	})
	return out
}

// expiryTime converts a Harbor expiry timestamp; non-positive values mean never.
func expiryTime(ts int64) *metav1.Time {
	if ts <= 0 {
		return nil
	}
	t := metav1.NewTime(time.Unix(ts, 0))
	return &t
}

// SetupWithManager sets up the controller with the Manager.
func (r *RobotAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupHarborResource(mgr, "robotaccount", &harborv1alpha1.RobotAccount{}, &harborv1alpha1.RobotAccountList{},
		func(o client.Object) string { return o.(*harborv1alpha1.RobotAccount).Spec.HarborConnectionRef.Name },
		func(o client.Object) []string {
			if ref := o.(*harborv1alpha1.RobotAccount).Spec.SecretRef; ref != nil {
				return []string{ref.Name}
			}
			return nil
		}, r)
}
