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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

const generatedPasswordLength = 32

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=users/finalizers,verbs=update

// Reconcile makes the Harbor user match the User spec.
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var user harborv1alpha1.User
	if err := r.Get(ctx, req.NamespacedName, &user); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	api, apiErr := harborClient(ctx, r.Client, r.ClientFactory, user.Namespace, user.Spec.HarborConnectionRef)

	if !user.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, finalize(ctx, r.Client, &user, user.Spec.DeletionPolicy, api, apiErr, func(api harbor.API) error {
			if user.Status.UserID == 0 {
				return nil
			}
			log.Info("Deleting Harbor user", "id", user.Status.UserID)
			return ignoreHarborNotFound(api.DeleteUser(ctx, user.Status.UserID))
		})
	}
	if err := ensureFinalizer(ctx, r.Client, &user); err != nil {
		return ctrl.Result{}, err
	}

	err := apiErr
	if err == nil {
		err = r.sync(ctx, api, &user)
	}
	if err != nil {
		log.Error(err, "Failed to reconcile Harbor user")
		setReady(&user.Status.HarborResourceStatus, user.Generation, metav1.ConditionFalse, failureReason(err, reasonHarborError), err.Error())
	} else {
		setReady(&user.Status.HarborResourceStatus, user.Generation, metav1.ConditionTrue, reasonReconciled, "User is in sync with Harbor")
	}
	if uerr := r.Status().Update(ctx, &user); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return resultFor(err)
}

func (r *UserReconciler) sync(ctx context.Context, api harbor.API, user *harborv1alpha1.User) error {
	spec := user.Spec
	if spec.Username == api.Username() {
		return fmt.Errorf("user %q is the HarborConnection user and cannot be managed", spec.Username)
	}

	password, err := r.password(ctx, user)
	if err != nil {
		return err
	}

	existing, err := findUser(ctx, api, user.Status.UserID, spec.Username)
	if err != nil {
		return err
	}

	realName := spec.RealName
	if realName == "" {
		realName = spec.Username
	}

	if existing == nil {
		id, err := api.CreateUser(ctx, harbor.UserCreate{
			Username: spec.Username,
			Email:    spec.Email,
			RealName: realName,
			Comment:  spec.Comment,
			Password: password,
		})
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Created Harbor user", "id", id)
		user.Status.UserID = id
		if user.Status.PasswordHash, err = hashSecret(password); err != nil {
			return err
		}
		existing = &harbor.User{UserID: id}
	} else {
		user.Status.UserID = existing.UserID
		if existing.Email != spec.Email || existing.RealName != realName || existing.Comment != spec.Comment {
			if err := api.UpdateUser(ctx, existing.UserID, harbor.UserProfile{
				Email: spec.Email, RealName: realName, Comment: spec.Comment,
			}); err != nil {
				return err
			}
		}
		if !secretMatchesHash(password, user.Status.PasswordHash) {
			if err := api.SetUserPassword(ctx, existing.UserID, password); err != nil && !harbor.IsSamePassword(err) {
				return err
			}
			if user.Status.PasswordHash, err = hashSecret(password); err != nil {
				return err
			}
		}
	}

	if existing.SysAdminFlag != spec.SysAdmin {
		return api.SetUserSysAdmin(ctx, existing.UserID, spec.SysAdmin)
	}
	return nil
}

// password returns the desired password, generating and storing one when no
// Secret is referenced.
func (r *UserReconciler) password(ctx context.Context, user *harborv1alpha1.User) (string, error) {
	if ref := user.Spec.PasswordSecretRef; ref != nil {
		user.Status.SecretName = ref.Name
		return readSecretKey(ctx, r.Client, user.Namespace, *ref)
	}

	name := generatedSecretName(user, user.Spec.GeneratedSecret)
	password, err := getGeneratedPassword(ctx, r.Client, user.Namespace, name)
	if err != nil {
		return "", err
	}
	if password == "" {
		if password, err = generatePassword(generatedPasswordLength); err != nil {
			return "", err
		}
	}
	if err := writeGeneratedSecret(ctx, r.Client, user, name, user.Spec.Username, password); err != nil {
		return "", err
	}
	user.Status.SecretName = name
	return password, nil
}

// findUser looks a user up by ID, falling back to the username. It returns
// nil when the user does not exist.
func findUser(ctx context.Context, api harbor.API, id int64, username string) (*harbor.User, error) {
	if id != 0 {
		u, err := api.GetUser(ctx, id)
		if err == nil && u.Username == username {
			return u, nil
		}
		if err != nil && !harbor.IsNotFound(err) {
			return nil, err
		}
	}
	u, err := api.FindUser(ctx, username)
	if harbor.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupHarborResource(mgr, "user", &harborv1alpha1.User{}, &harborv1alpha1.UserList{},
		func(o client.Object) string { return o.(*harborv1alpha1.User).Spec.HarborConnectionRef.Name },
		func(o client.Object) []string {
			if ref := o.(*harborv1alpha1.User).Spec.PasswordSecretRef; ref != nil {
				return []string{ref.Name}
			}
			return nil
		}, r)
}
