# Getting started

This guide installs the operator with Helm, connects it to Harbor, and creates
a project with a robot account that CI can push with.

## Requirements

- Kubernetes 1.25 or later.
- Harbor 2.x. Tested against Harbor 2.15.
- A Harbor system administrator account for the operator.
- `User` resources need Harbor's database authentication mode (`db_auth`).
  Groups must match the auth mode in use: `LDAP` groups with LDAP auth, `OIDC`
  groups with OIDC auth, `HTTP` groups with HTTP auth.

## 1. Install the operator

```sh
helm install harbor-operator ./charts/harbor-operator \
  --namespace harbor-operator-system --create-namespace \
  --set image.repository=<registry>/harbor-operator \
  --set image.tag=<tag>

kubectl -n harbor-operator-system rollout status deploy/harbor-operator
```

Chart options are described in the [chart README](../charts/harbor-operator/README.md).

## 2. Connect to Harbor

Create the admin password Secret and a `HarborConnection` in the namespace
where you will create Harbor resources:

```sh
kubectl create namespace platform
kubectl -n platform create secret generic harbor-admin \
  --from-literal=password='<admin password>'
```

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: HarborConnection
metadata:
  name: harbor
  namespace: platform
spec:
  url: https://harbor.example.com
  passwordSecretRef:
    name: harbor-admin
```

```sh
kubectl -n platform get harborconnection harbor
# NAME     URL                          VERSION   READY
# harbor   https://harbor.example.com   v2.15.4   True
```

If `READY` is `False`, `kubectl describe` shows the reason, such as a wrong
password (401), an unreachable URL, or a user that is not a system administrator.

For a private CA, put the PEM bundle in a Secret and set
`spec.caSecretRef: {name: harbor-ca, key: ca.crt}`.

## 3. Create a project

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: Project
metadata:
  name: team-a
  namespace: platform
spec:
  harborConnectionRef:
    name: harbor
  name: team-a
  storageQuota: 50Gi
  policies:
    autoScan: true
    preventVulnerable: true
    severity: high
```

## 4. Create a robot account for CI

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: RobotAccount
metadata:
  name: team-a-ci
  namespace: platform
spec:
  harborConnectionRef:
    name: harbor
  name: ci
  level: project
  project: team-a
  permissions:
    - access:
        - { resource: repository, action: pull }
        - { resource: repository, action: push }
```

Harbor generates the secret; the operator stores it in the Secret
`team-a-ci-harbor-credentials`:

```sh
kubectl -n platform get robotaccount team-a-ci
# NAME        ROBOT             LEVEL     PROJECT   READY
# team-a-ci   robot$team-a+ci   project   team-a    True

user=$(kubectl -n platform get secret team-a-ci-harbor-credentials -o jsonpath='{.data.username}' | base64 -d)
kubectl -n platform get secret team-a-ci-harbor-credentials -o jsonpath='{.data.password}' | base64 -d |
  docker login harbor.example.com -u "$user" --password-stdin
```

## Next steps

- [Resource reference](resources.md): every field of every resource.
- [config/samples](../config/samples): one example of each resource.
- [Testing](testing.md): run the test suites, including against a real Harbor in kind.
