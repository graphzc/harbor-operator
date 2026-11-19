# harbor-operator Helm chart

Installs [harbor-operator](../../README.md), which manages Harbor users, groups,
projects and robot accounts through Kubernetes resources.

## Install

From a release:

```sh
helm install harbor-operator oci://ghcr.io/graphzc/charts/harbor-operator --version <version> \
  --namespace harbor-operator-system --create-namespace
```

From source, with your own image:

```sh
helm install harbor-operator ./charts/harbor-operator \
  --namespace harbor-operator-system --create-namespace \
  --set image.repository=<registry>/harbor-operator \
  --set image.tag=<tag>
```

Then connect it to Harbor. See [Getting started](../../docs/getting-started.md).

## CRDs

The CRDs are templates in this chart (`templates/crds/`), not files in the
`crds/` directory, so `helm upgrade` updates them along with the operator.

- `crds.install: false` skips them, for when CRDs are managed separately (for
  example with `make install`).
- `crds.keep: true` (the default) adds `helm.sh/resource-policy: keep`, so
  `helm uninstall` leaves the CRDs in place. Deleting a CRD deletes every
  resource of that kind. Each resource's finalizer then deletes its object in
  Harbor (unless its `deletionPolicy` is `Orphan`), which is rarely what you
  want during an uninstall.

The CRD templates and the manager's ClusterRole rules are generated. Do not
edit them by hand: after changing the API types or RBAC markers, run
`make helm-sync`. `make helm-verify` fails when the chart is out of date.

## Values

| Key | Default | Description |
|-----|---------|-------------|
| `replicaCount` | `1` | Operator replicas. Only the elected leader reconciles. |
| `image.repository` | `ghcr.io/graphzc/harbor-operator` | Operator image. |
| `image.tag` | chart `appVersion` | Image tag. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Secrets for pulling the image. |
| `crds.install` | `true` | Install and upgrade the CRDs. |
| `crds.keep` | `true` | Keep the CRDs on uninstall. |
| `watchNamespaces` | `[]` | Namespaces to manage. Empty means all namespaces. |
| `leaderElection.enabled` | `true` | Leader election. Required when `replicaCount > 1`. |
| `logging.level` | `info` | `debug`, `info` or `error`. |
| `logging.encoder` | `json` | `json` or `console`. |
| `extraArgs` | `[]` | Extra manager flags. |
| `extraEnv` | `[]` | Extra environment variables. |
| `serviceAccount.create` | `true` | Create the ServiceAccount. |
| `serviceAccount.name` | `""` | ServiceAccount name; generated when empty. |
| `serviceAccount.annotations` | `{}` | ServiceAccount annotations. |
| `rbac.create` | `true` | Create the manager ClusterRole, leader-election Role and bindings. |
| `rbac.aggregateToDefaultRoles` | `true` | Grant the built-in `admin`/`edit` roles read/write and `view` read-only access to Harbor resources. |
| `metrics.enabled` | `true` | Expose metrics through the `<release>-metrics` Service. |
| `metrics.secure` | `true` | Serve metrics over HTTPS with Kubernetes authn/authz. Bind the `<release>-metrics-reader` ClusterRole to your scraper. |
| `metrics.port` | `8443` | Metrics port. |
| `podAnnotations`, `podLabels` | `{}` | Extra pod metadata. |
| `podSecurityContext` | non-root, `RuntimeDefault` seccomp | Pod security context. |
| `securityContext` | read-only root, no privilege escalation, all capabilities dropped | Container security context. |
| `resources` | 10m/64Mi requests, 500m/256Mi limits | Container resources. |
| `nodeSelector`, `tolerations`, `affinity` | empty | Scheduling. |
| `priorityClassName` | `""` | Pod priority class. |

### Watching specific namespaces

By default the operator watches, and caches, every Secret in the cluster,
because resources reference Secrets by name. In large clusters, set
`watchNamespaces` to the namespaces that hold Harbor resources:

```yaml
watchNamespaces:
  - platform
  - team-a
```

The ClusterRole is still cluster-wide; `watchNamespaces` only limits what
the operator watches and acts on.

## Uninstall

```sh
helm uninstall harbor-operator -n harbor-operator-system
```

Delete the Harbor resources first if you want their Harbor objects removed:
the finalizers need the operator running.
