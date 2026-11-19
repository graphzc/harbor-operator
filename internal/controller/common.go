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
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

const (
	// resyncPeriod is how often resources are re-reconciled to correct drift
	// made directly in Harbor.
	resyncPeriod = 10 * time.Minute
	// errorRequeue is the delay before retrying after a dependency is missing.
	errorRequeue = 30 * time.Second

	// Index keys used to map Secrets and HarborConnections back to resources.
	secretRefIndex     = ".spec.secretRefs"
	connectionRefIndex = ".spec.harborConnectionRef.name"

	secretKeyUsername = "username"
	secretKeyPassword = "password"

	// Condition reasons.
	reasonReconciled      = "Reconciled"
	reasonConnectionError = "ConnectionError"
	reasonSecretError     = "SecretError"
	reasonHarborError     = "HarborError"
	reasonDeleteError     = "DeleteError"
)

// ClientFactory builds a Harbor API client. It is a variable on reconcilers
// so tests can substitute it.
type ClientFactory func(harbor.Config) (harbor.API, error)

// DefaultClientFactory builds a real Harbor REST client.
func DefaultClientFactory(cfg harbor.Config) (harbor.API, error) {
	return harbor.NewClient(cfg)
}

// errNotFoundDependency marks errors caused by a missing Kubernetes object,
// which are retried after errorRequeue rather than with backoff.
type errNotFoundDependency struct{ err error }

func (e errNotFoundDependency) Error() string { return e.err.Error() }
func (e errNotFoundDependency) Unwrap() error { return e.err }

// harborClient resolves a HarborConnection reference into a Harbor client.
func harborClient(ctx context.Context, c client.Client, factory ClientFactory, namespace string,
	ref harborv1alpha1.HarborConnectionReference) (harbor.API, error) {
	var conn harborv1alpha1.HarborConnection
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, errNotFoundDependency{fmt.Errorf("HarborConnection %q not found", ref.Name)}
		}
		return nil, err
	}
	return connectionClient(ctx, c, factory, &conn)
}

// connectionClient builds a Harbor client from a HarborConnection.
func connectionClient(ctx context.Context, c client.Client, factory ClientFactory,
	conn *harborv1alpha1.HarborConnection) (harbor.API, error) {
	password, err := readSecretKey(ctx, c, conn.Namespace, conn.Spec.PasswordSecretRef)
	if err != nil {
		return nil, err
	}
	cfg := harbor.Config{
		URL:                conn.Spec.URL,
		Username:           conn.Spec.Username,
		Password:           password,
		InsecureSkipVerify: conn.Spec.InsecureSkipVerify,
	}
	if cfg.Username == "" {
		cfg.Username = "admin"
	}
	if conn.Spec.CASecretRef != nil {
		ca, err := readSecretKey(ctx, c, conn.Namespace, *conn.Spec.CASecretRef)
		if err != nil {
			return nil, err
		}
		cfg.CABundle = []byte(ca)
	}
	if factory == nil {
		factory = DefaultClientFactory
	}
	return factory(cfg)
}

// readSecretKey returns the value of a key in a Secret.
func readSecretKey(ctx context.Context, c client.Client, namespace string, sel harborv1alpha1.SecretKeySelector) (string, error) {
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: sel.Name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", errNotFoundDependency{fmt.Errorf("secret %q not found", sel.Name)}
		}
		return "", err
	}
	v, ok := secret.Data[sel.GetKey()]
	if !ok || len(v) == 0 {
		return "", errNotFoundDependency{fmt.Errorf("secret %q has no key %q", sel.Name, sel.GetKey())}
	}
	return string(v), nil
}

// generatedSecretName returns the name of the Secret holding generated credentials.
func generatedSecretName(owner client.Object, gs harborv1alpha1.GeneratedSecret) string {
	if gs.Name != "" {
		return gs.Name
	}
	return owner.GetName() + "-harbor-credentials"
}

// getGeneratedPassword returns the password stored in the generated Secret,
// or "" when the Secret or key does not exist.
func getGeneratedPassword(ctx context.Context, c client.Client, namespace, name string) (string, error) {
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return string(secret.Data[secretKeyPassword]), nil
}

// writeGeneratedSecret creates or updates the Secret holding credentials,
// owned by owner so it is garbage collected with it.
func writeGeneratedSecret(ctx context.Context, c client.Client, owner client.Object, name, username, password string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: owner.GetNamespace(), Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if !secret.CreationTimestamp.IsZero() && !metav1.IsControlledBy(secret, owner) {
			return fmt.Errorf("secret %q already exists and is not managed by %q", name, owner.GetName())
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels["app.kubernetes.io/managed-by"] = "harbor-operator"
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			secretKeyUsername: []byte(username),
			secretKeyPassword: []byte(password),
		}
		return controllerutil.SetControllerReference(owner, secret, c.Scheme())
	})
	return err
}

const (
	lowerChars = "abcdefghijklmnopqrstuvwxyz"
	upperChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitChars = "0123456789"
)

// generatePassword returns a random password that satisfies Harbor's policy
// (8-128 characters with at least one lower case, upper case and digit).
func generatePassword(length int) (string, error) {
	all := lowerChars + upperChars + digitChars
	sets := []string{lowerChars, upperChars, digitChars}
	out := make([]byte, length)
	for i := range out {
		set := all
		if i < len(sets) {
			set = sets[i]
		}
		ch, err := randChar(set)
		if err != nil {
			return "", err
		}
		out[i] = ch
	}
	// Shuffle so the guaranteed characters are not always at the start.
	for i := len(out) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return string(out), nil
}

func randChar(set string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, err
	}
	return set[n.Int64()], nil
}

// hashSecret returns a bcrypt hash of a credential so status can record what
// was applied without exposing it.
func hashSecret(secret string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	return string(h), err
}

// secretMatchesHash reports whether secret matches a hash from hashSecret.
func secretMatchesHash(secret, hash string) bool {
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}

// setReady records the Ready condition on a status.
func setReady(st *harborv1alpha1.HarborResourceStatus, generation int64, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&st.Conditions, metav1.Condition{
		Type:               harborv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: generation,
	})
	if status == metav1.ConditionTrue {
		st.ObservedGeneration = generation
	}
}

// failureReason classifies an error into a condition reason.
func failureReason(err error, fallback string) string {
	var dep errNotFoundDependency
	var herr *harbor.Error
	switch {
	case errors.As(err, &dep):
		return reasonConnectionError
	case errors.As(err, &herr):
		return reasonHarborError
	default:
		return fallback
	}
}

// resultFor turns a reconcile error into a result. Missing dependencies are
// retried on a fixed delay instead of exponential backoff.
func resultFor(err error) (ctrl.Result, error) {
	if err == nil {
		return ctrl.Result{RequeueAfter: resyncPeriod}, nil
	}
	var dep errNotFoundDependency
	if errors.As(err, &dep) {
		return ctrl.Result{RequeueAfter: errorRequeue}, nil
	}
	return ctrl.Result{}, err
}

// ignoreHarborNotFound returns nil for Harbor 404 errors.
func ignoreHarborNotFound(err error) error {
	if harbor.IsNotFound(err) {
		return nil
	}
	return err
}

// enqueueByIndex maps an object to the resources in its namespace whose index
// field contains the object's name.
func enqueueByIndex(c client.Client, list client.ObjectList, index string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		l := list.DeepCopyObject().(client.ObjectList)
		if err := c.List(ctx, l, client.InNamespace(obj.GetNamespace()), client.MatchingFields{index: obj.GetName()}); err != nil {
			return nil
		}
		items, err := meta.ExtractList(l)
		if err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(items))
		for _, item := range items {
			o := item.(client.Object)
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(o)})
		}
		return reqs
	})
}

// finalize runs cleanup for a resource being deleted and removes the finalizer.
// When the HarborConnection or its credentials no longer exist the Harbor
// object cannot be reached, so the finalizer is removed without cleanup.
func finalize(ctx context.Context, c client.Client, obj client.Object, policy harborv1alpha1.DeletionPolicy,
	api harbor.API, apiErr error, cleanup func(harbor.API) error) error {
	if !controllerutil.ContainsFinalizer(obj, harborv1alpha1.Finalizer) {
		return nil
	}
	if policy != harborv1alpha1.DeletionPolicyOrphan {
		var dep errNotFoundDependency
		switch {
		case errors.As(apiErr, &dep):
			ctrl.LoggerFrom(ctx).Info("Harbor connection unavailable, skipping cleanup", "reason", apiErr.Error())
		case apiErr != nil:
			return apiErr
		default:
			if err := cleanup(api); err != nil {
				return err
			}
		}
	}
	controllerutil.RemoveFinalizer(obj, harborv1alpha1.Finalizer)
	return c.Update(ctx, obj)
}

// ensureFinalizer adds the finalizer to obj if it is missing.
func ensureFinalizer(ctx context.Context, c client.Client, obj client.Object) error {
	if controllerutil.AddFinalizer(obj, harborv1alpha1.Finalizer) {
		return c.Update(ctx, obj)
	}
	return nil
}

// setupHarborResource registers a controller for a Harbor-backed resource.
// Resources are re-queued when their HarborConnection changes, when a Secret
// returned by secretRefs changes, and (via ownership) when a generated
// Secret changes.
func setupHarborResource(mgr ctrl.Manager, name string, obj client.Object, list client.ObjectList,
	connRef func(client.Object) string, secretRefs func(client.Object) []string, r reconcile.Reconciler) error {
	ctx := context.Background()
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(ctx, obj, connectionRefIndex, func(o client.Object) []string {
		return []string{connRef(o)}
	}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, obj, secretRefIndex, secretRefs); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(obj, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Secret{}).
		Watches(&harborv1alpha1.HarborConnection{}, enqueueByIndex(mgr.GetClient(), list, connectionRefIndex)).
		Watches(&corev1.Secret{}, enqueueByIndex(mgr.GetClient(), list, secretRefIndex)).
		Named(name).
		Complete(r)
}
