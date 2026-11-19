# Testing

There are three levels of tests.

| Command | What it runs | Needs |
|---------|--------------|-------|
| `make test` | Unit tests and controller tests in envtest against an in-memory fake Harbor | Go |
| `make lint` | golangci-lint | Go |
| `make kind-e2e` | The Helm chart and operator against a real Harbor in kind | Docker, kind, kubectl, helm, curl, python3 |

## Controller tests

`make test` downloads the envtest binaries (kube-apiserver and etcd), starts
the five controllers, and runs them against
[harbortest](../internal/harbor/harbortest/server.go), an in-memory Harbor API.
The tests also dry-run every manifest in `config/samples` against the CRD schemas.

The fake copies Harbor behavior that the controllers depend on, such as single
`GET /usergroups/{id}` responses having no ID, `GET /robots` listing only
system robots unless a project is selected, and rejecting a password change
to the current password. When the real Harbor behaves differently from the
fake, update the fake and add a test.

## End-to-end tests in kind

```sh
make kind-e2e                    # creates and deletes the cluster
KEEP_CLUSTER=true make kind-e2e  # keeps it for inspection; reruns reuse it
```

[hack/kind-e2e.sh](../hack/kind-e2e.sh):

1. Creates the kind cluster `harbor-operator-e2e` (override with `KIND_CLUSTER`).
2. Installs Harbor with the official `harbor/harbor` chart (version set by
   `HARBOR_CHART_VERSION`, default 1.19.4 / Harbor 2.15.4), without persistence
   or Trivy.
3. Builds the operator image, loads it into kind, and installs
   `charts/harbor-operator` with `watchNamespaces` set to the test namespace.
4. Applies every sample in `config/samples` and waits for `Ready`.
5. Verifies through the Harbor API:
   - The project's policies, CVE allowlist, quota, and member roles.
   - The user logs in with the generated password.
   - The project robot's registry token grants `pull,push` on its project.
   - The system robot's token grants only `pull`, using the referenced secret.
6. Rotates the system robot's Secret and checks that the new secret works and
   the old one is rejected.
7. Updates the project: makes it public, removes the group member, changes a
   role.
8. Deletes everything and checks the objects are gone from Harbor and the
   generated Secrets are garbage collected.

The script always passes `--context kind-<cluster>`, so it never touches your
current kubectl context.

Harbor publishes `linux/amd64` images only. The script pulls them for amd64
and loads them into kind, so on Apple silicon they run under Docker
Desktop's emulation. Harbor is slower that way, but it works. Give Docker
at least 6 GB of memory. The first run downloads about 1 GB of images and
takes around 10 minutes; reruns with `KEEP_CLUSTER=true` take about 2 minutes.

To inspect a kept cluster:

```sh
kubectl --context kind-harbor-operator-e2e -n harbor-operator-system logs deploy/harbor-operator
kubectl --context kind-harbor-operator-e2e -n harbor port-forward svc/harbor 18080:80
# Harbor UI and API: http://localhost:18080 (admin / Harbor12345)
kind delete cluster --name harbor-operator-e2e
```
