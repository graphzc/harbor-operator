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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	harborv1alpha1 "github.com/graphzc/harbor-operator/api/v1alpha1"
	"github.com/graphzc/harbor-operator/internal/harbor"
)

// ConfigurationReconciler reconciles a Configuration object
type ConfigurationReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=configurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=configurations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=harbor.graphzc.io,resources=configurations/finalizers,verbs=update

// Reconcile makes the Harbor system configuration match the Configuration
// spec. Harbor configuration cannot be deleted, so no finalizer is used.
func (r *ConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cfg harborv1alpha1.Configuration
	if err := r.Get(ctx, req.NamespacedName, &cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	api, err := harborClient(ctx, r.Client, r.ClientFactory, cfg.Namespace, cfg.Spec.HarborConnectionRef)
	if err == nil {
		err = r.sync(ctx, api, &cfg)
	}
	if err != nil {
		log.Error(err, "Failed to reconcile Harbor configuration")
		setReady(&cfg.Status.HarborResourceStatus, cfg.Generation, metav1.ConditionFalse, failureReason(err, reasonHarborError), err.Error())
	} else {
		setReady(&cfg.Status.HarborResourceStatus, cfg.Generation, metav1.ConditionTrue, reasonReconciled, "Configuration is in sync with Harbor")
	}
	if uerr := r.Status().Update(ctx, &cfg); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return resultFor(err)
}

func (r *ConfigurationReconciler) sync(ctx context.Context, api harbor.API, cfg *harborv1alpha1.Configuration) error {
	spec := cfg.Spec
	specKeys, err := harborConfigKeys(spec)
	if err != nil {
		return err
	}

	// Read the credentials first so a missing Secret is reported as such.
	// Below, secrets and update are keyed by Harbor (snake_case) key.
	secrets := make(map[string]string, len(spec.SecretSettings))
	for k, sel := range spec.SecretSettings {
		v, err := readSecretKey(ctx, r.Client, cfg.Namespace, sel)
		if err != nil {
			return err
		}
		secrets[harborConfigKey(k)] = v
	}

	current, err := api.GetConfigurations(ctx)
	if err != nil {
		return err
	}
	update := map[string]json.RawMessage{}
	for _, k := range slices.Sorted(maps.Keys(spec.Settings)) {
		hk := harborConfigKey(k)
		want := spec.Settings[k].Raw
		cur, ok := current[hk]
		if ok && configValueEqual(cur.Value, want) {
			continue
		}
		if ok && !cur.Editable {
			return fmt.Errorf("configuration %q (%s) is not editable in Harbor", k, hk)
		}
		update[hk] = want
	}

	digest := secretSettingsDigest(secrets)
	secretsChanged := len(secrets) > 0 && !secretMatchesHash(digest, cfg.Status.SecretSettingsHash)
	if secretsChanged {
		for k, v := range secrets {
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			update[k] = raw
		}
	}

	if len(update) > 0 {
		if err := api.UpdateConfigurations(ctx, update); err != nil {
			return err
		}
		updated := make([]string, 0, len(update))
		for hk := range update {
			updated = append(updated, specKeys[hk])
		}
		slices.Sort(updated)
		logf.FromContext(ctx).Info("Updated Harbor configuration", "keys", updated)
	}

	switch {
	case len(secrets) == 0:
		cfg.Status.SecretSettingsHash = ""
	case secretsChanged:
		hash, err := hashSecret(digest)
		if err != nil {
			return err
		}
		cfg.Status.SecretSettingsHash = hash
	}
	return nil
}

// configKeyPattern matches the camelCase keys accepted in a Configuration.
var configKeyPattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// harborConfigKeys validates the spec keys and maps each Harbor key to the
// spec key that sets it, e.g. "project_creation_restriction" to
// "settings.projectCreationRestriction". CEL cannot see the schemaless
// settings map, so this is checked here rather than at admission.
func harborConfigKeys(spec harborv1alpha1.ConfigurationSpec) (map[string]string, error) {
	keys := map[string]string{}
	add := func(field string, m []string) error {
		for _, k := range m {
			if !configKeyPattern.MatchString(k) {
				return fmt.Errorf("%s key %q must be camelCase, e.g. %q", field, k, camelConfigKey(k))
			}
			hk := harborConfigKey(k)
			if prev, ok := keys[hk]; ok {
				return fmt.Errorf("%s and %s.%s both set Harbor configuration %s", prev, field, k, hk)
			}
			keys[hk] = field + "." + k
		}
		return nil
	}
	if err := add("settings", slices.Sorted(maps.Keys(spec.Settings))); err != nil {
		return nil, err
	}
	if err := add("secretSettings", slices.Sorted(maps.Keys(spec.SecretSettings))); err != nil {
		return nil, err
	}
	return keys, nil
}

// harborConfigKey converts a camelCase key to Harbor's snake_case key. A run
// of capitals is one word, so ldapURL and ldapUrl both become ldap_url.
func harborConfigKey(key string) string {
	var b strings.Builder
	runes := []rune(key)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (!unicode.IsUpper(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// camelConfigKey suggests the camelCase form of an invalid key such as a
// Harbor snake_case key.
func camelConfigKey(key string) string {
	words := strings.FieldsFunc(key, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for i, w := range words {
		first, rest := []rune(w)[0], string([]rune(w)[1:])
		if i == 0 {
			b.WriteRune(unicode.ToLower(first))
		} else {
			b.WriteRune(unicode.ToUpper(first))
		}
		b.WriteString(rest)
	}
	return b.String()
}

// configValueEqual reports whether two configuration values are the same.
// Scalars are compared by their string form, because Harbor accepts "30" for
// an integer setting but returns 30.
func configValueEqual(current, desired json.RawMessage) bool {
	return configValueString(current) == configValueString(desired)
}

func configValueString(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		out, err := json.Marshal(v)
		if err != nil {
			return string(raw)
		}
		return string(out)
	}
}

// secretSettingsDigest returns a SHA-256 of the credential settings. bcrypt
// only reads the first 72 bytes of its input, so the digest is what gets hashed.
func secretSettingsDigest(secrets map[string]string) string {
	h := sha256.New()
	for _, k := range slices.Sorted(maps.Keys(secrets)) {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(secrets[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigurationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return setupHarborResource(mgr, "configuration", &harborv1alpha1.Configuration{}, &harborv1alpha1.ConfigurationList{},
		func(o client.Object) string { return o.(*harborv1alpha1.Configuration).Spec.HarborConnectionRef.Name },
		func(o client.Object) []string {
			cfg := o.(*harborv1alpha1.Configuration)
			names := make([]string, 0, len(cfg.Spec.SecretSettings))
			for _, sel := range cfg.Spec.SecretSettings {
				names = append(names, sel.Name)
			}
			return names
		}, r)
}
