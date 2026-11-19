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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
)

// HarborConnectionReconciler verifies connectivity to a Harbor instance.
type HarborConnectionReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=harborconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=harborconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=harborconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile checks that Harbor is reachable and that the credentials belong
// to a system administrator.
func (r *HarborConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var conn harborv1alpha1.HarborConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := r.check(ctx, &conn)
	now := metav1.Now()
	conn.Status.LastCheckedTime = &now
	if err != nil {
		log.Error(err, "Harbor connection check failed")
		setReady(&conn.Status.HarborResourceStatus, conn.Generation, metav1.ConditionFalse,
			failureReason(err, reasonConnectionError), err.Error())
	} else {
		setReady(&conn.Status.HarborResourceStatus, conn.Generation, metav1.ConditionTrue,
			reasonReconciled, "Connected to Harbor")
	}
	if uerr := r.Status().Update(ctx, &conn); uerr != nil {
		return ctrl.Result{}, uerr
	}
	if err != nil {
		// Connection problems are usually external; retry on a fixed delay.
		return ctrl.Result{RequeueAfter: errorRequeue}, nil
	}
	return ctrl.Result{RequeueAfter: resyncPeriod}, nil
}

func (r *HarborConnectionReconciler) check(ctx context.Context, conn *harborv1alpha1.HarborConnection) error {
	api, err := connectionClient(ctx, r.Client, r.ClientFactory, conn)
	if err != nil {
		return err
	}
	info, err := api.SystemInfo(ctx)
	if err != nil {
		return err
	}
	conn.Status.HarborVersion = info.HarborVersion
	conn.Status.AuthMode = info.AuthMode

	me, err := api.CurrentUser(ctx)
	if err != nil {
		return err
	}
	if !me.SysAdminFlag {
		return fmt.Errorf("user %q is not a Harbor system administrator", me.Username)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HarborConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &harborv1alpha1.HarborConnection{}, secretRefIndex,
		func(obj client.Object) []string {
			conn := obj.(*harborv1alpha1.HarborConnection)
			names := []string{conn.Spec.PasswordSecretRef.Name}
			if conn.Spec.CASecretRef != nil {
				names = append(names, conn.Spec.CASecretRef.Name)
			}
			return names
		}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&harborv1alpha1.HarborConnection{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Secret{}, enqueueByIndex(mgr.GetClient(), &harborv1alpha1.HarborConnectionList{}, secretRefIndex)).
		Named("harborconnection").
		Complete(r)
}
