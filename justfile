# Run `just` to list the recipes.
#
# Variables can be overridden from the environment or on the command line,
# before the recipe name:
#   IMG=example.com/harbor-operator:v1 just deploy
#   just IMG=example.com/harbor-operator:v1 deploy

set shell := ["bash", "-euo", "pipefail", "-c"]

# Image URL used by the build, push and deploy recipes.
IMG := env("IMG", "controller:latest")

# Tool used to build images. Only Docker is tested; podman may work.
CONTAINER_TOOL := env("CONTAINER_TOOL", "docker")

# Platforms for docker-buildx. Building for several platforms requires buildx,
# BuildKit, and a registry you can push to (set IMG).
PLATFORMS := env("PLATFORMS", "linux/arm64,linux/amd64")

# Kind cluster used by test-e2e. The cluster is created if it does not exist.
KIND_CLUSTER := env("KIND_CLUSTER", "harbor-operator-test-e2e")

# Bundle and catalog (OLM) settings.
# VERSION is the bundle version, e.g. `just VERSION=0.0.2 bundle`.
# CHANNELS (e.g. "candidate,fast,stable") and DEFAULT_CHANNEL are passed to
# `operator-sdk generate bundle` when set.
VERSION := env("VERSION", "0.0.1")
CHANNELS := env("CHANNELS", "")
DEFAULT_CHANNEL := env("DEFAULT_CHANNEL", "")
# Registry namespace and name prefix for the bundle and catalog images.
IMAGE_TAG_BASE := env("IMAGE_TAG_BASE", "graphzc.io/harbor-operator")
BUNDLE_IMG := env("BUNDLE_IMG", IMAGE_TAG_BASE + "-bundle:v" + VERSION)
# Set to true to reference images by SHA digest instead of tag in the bundle.
USE_IMAGE_DIGESTS := env("USE_IMAGE_DIGESTS", "false")
# Comma-separated bundle images to add to the catalog. They must be pullable.
BUNDLE_IMGS := env("BUNDLE_IMGS", BUNDLE_IMG)
CATALOG_IMG := env("CATALOG_IMG", IMAGE_TAG_BASE + "-catalog:v" + VERSION)
# Existing catalog image to add BUNDLE_IMGS to. Empty starts a new catalog.
CATALOG_BASE_IMG := env("CATALOG_BASE_IMG", "")

# Tools. Go tools are installed into bin/ at the pinned versions.
LOCALBIN := justfile_directory() / "bin"
KUBECTL := env("KUBECTL", "kubectl")
KIND := env("KIND", "kind")
KUSTOMIZE := LOCALBIN / "kustomize"
CONTROLLER_GEN := LOCALBIN / "controller-gen"
ENVTEST := LOCALBIN / "setup-envtest"
GOLANGCI_LINT := LOCALBIN / "golangci-lint"

KUSTOMIZE_VERSION := env("KUSTOMIZE_VERSION", "v5.6.0")
CONTROLLER_TOOLS_VERSION := env("CONTROLLER_TOOLS_VERSION", "v0.18.0")
GOLANGCI_LINT_VERSION := env("GOLANGCI_LINT_VERSION", "v2.14.0")
OPERATOR_SDK_VERSION := env("OPERATOR_SDK_VERSION", "v1.42.3")
OPM_VERSION := env("OPM_VERSION", "v1.55.0")
# ENVTEST_VERSION (controller-runtime release branch, e.g. release-0.20) and
# ENVTEST_K8S_VERSION (e.g. 1.31) default to the versions in go.mod; set them
# in the environment to override.

JUST := quote(just_executable()) + " --justfile " + quote(justfile())

[private]
default:
    @{{JUST}} --list --unsorted

# ---------------------------------------------------------------- Development

[group('development')]
[doc('Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.')]
manifests: controller-gen
    "{{CONTROLLER_GEN}}" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

[group('development')]
[doc('Generate DeepCopy, DeepCopyInto and DeepCopyObject implementations.')]
generate: controller-gen
    "{{CONTROLLER_GEN}}" object:headerFile="hack/boilerplate.go.txt" paths="./..."

[group('development')]
[doc('Run go fmt.')]
fmt:
    go fmt ./...

[group('development')]
[doc('Run go vet.')]
vet:
    go vet ./...

[group('development')]
[doc('Run the unit and envtest controller tests.')]
test: manifests generate fmt vet
    #!/usr/bin/env bash
    set -euo pipefail
    assets="$({{JUST}} setup-envtest)"
    KUBEBUILDER_ASSETS="$assets" go test $(go list ./... | grep -v /e2e) -coverprofile cover.out

[group('development')]
[doc('Create the Kind cluster for test-e2e if it does not exist.')]
setup-test-e2e:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v "{{KIND}}" >/dev/null 2>&1; then
      echo "Kind is not installed. Please install Kind manually." >&2
      exit 1
    fi
    case "$("{{KIND}}" get clusters)" in
      *"{{KIND_CLUSTER}}"*)
        echo "Kind cluster '{{KIND_CLUSTER}}' already exists. Skipping creation." ;;
      *)
        echo "Creating Kind cluster '{{KIND_CLUSTER}}'..."
        "{{KIND}}" create cluster --name "{{KIND_CLUSTER}}" ;;
    esac

[group('development')]
[doc('Run the scaffolded e2e tests in Kind, then delete the cluster.')]
test-e2e: setup-test-e2e manifests generate fmt vet && cleanup-test-e2e
    KIND_CLUSTER="{{KIND_CLUSTER}}" go test ./test/e2e/ -v -ginkgo.v

[group('development')]
[doc('Delete the Kind cluster used by test-e2e.')]
cleanup-test-e2e:
    @"{{KIND}}" delete cluster --name "{{KIND_CLUSTER}}"

[group('development')]
[doc('Run golangci-lint.')]
lint: golangci-lint
    "{{GOLANGCI_LINT}}" run

[group('development')]
[doc('Run golangci-lint and apply fixes.')]
lint-fix: golangci-lint
    "{{GOLANGCI_LINT}}" run --fix

[group('development')]
[doc('Verify the golangci-lint configuration.')]
lint-config: golangci-lint
    "{{GOLANGCI_LINT}}" config verify

# ---------------------------------------------------------------------- Build

[group('build')]
[doc('Build the manager binary.')]
build: manifests generate fmt vet
    go build -o bin/manager cmd/main.go

[group('build')]
[doc('Run the controller from your host against the current kubeconfig.')]
run: manifests generate fmt vet
    go run ./cmd/main.go

[group('build')]
[doc('Build the manager image.')]
docker-build:
    "{{CONTAINER_TOOL}}" build -t "{{IMG}}" .

[group('build')]
[doc('Push an image (default: IMG).')]
docker-push img=IMG:
    "{{CONTAINER_TOOL}}" push "{{img}}"

[group('build')]
[doc('Build and push the manager image for PLATFORMS.')]
docker-buildx:
    -"{{CONTAINER_TOOL}}" buildx create --name harbor-operator-builder
    "{{CONTAINER_TOOL}}" buildx use harbor-operator-builder
    -"{{CONTAINER_TOOL}}" buildx build --push --platform="{{PLATFORMS}}" --tag "{{IMG}}" .
    -"{{CONTAINER_TOOL}}" buildx rm harbor-operator-builder

[group('build')]
[doc('Generate dist/install.yaml with the CRDs and the deployment.')]
build-installer: manifests generate kustomize
    mkdir -p dist
    cd config/manager && "{{KUSTOMIZE}}" edit set image controller="{{IMG}}"
    "{{KUSTOMIZE}}" build config/default > dist/install.yaml

# ----------------------------------------------------------------------- Helm

[group('helm')]
[doc('Copy the generated CRDs and RBAC rules into charts/harbor-operator.')]
helm-sync: manifests
    ./hack/helm-sync.sh

[group('helm')]
[doc('Lint the Helm chart.')]
helm-lint: helm-sync
    helm lint charts/harbor-operator

[group('helm')]
[doc('Fail if the chart is out of sync with the generated manifests.')]
helm-verify: helm-sync
    git diff --exit-code -- charts/harbor-operator || { echo "Chart is out of date: run 'just helm-sync' and commit." >&2; exit 1; }

[group('helm')]
[doc('Run the end-to-end test against a real Harbor in kind (KEEP_CLUSTER=true keeps the cluster).')]
kind-e2e:
    ./hack/kind-e2e.sh

# ----------------------------------------------------------------- Deployment

[group('deployment')]
[doc('Install the CRDs into the cluster in ~/.kube/config.')]
install: manifests kustomize
    "{{KUSTOMIZE}}" build config/crd | "{{KUBECTL}}" apply -f -

[group('deployment')]
[doc('Uninstall the CRDs. `just uninstall true` ignores not-found errors.')]
uninstall ignore_not_found="false": manifests kustomize
    "{{KUSTOMIZE}}" build config/crd | "{{KUBECTL}}" delete --ignore-not-found={{ignore_not_found}} -f -

[group('deployment')]
[doc('Deploy the controller (image IMG) to the cluster in ~/.kube/config.')]
deploy: manifests kustomize
    cd config/manager && "{{KUSTOMIZE}}" edit set image controller="{{IMG}}"
    "{{KUSTOMIZE}}" build config/default | "{{KUBECTL}}" apply -f -

[group('deployment')]
[doc('Undeploy the controller. `just undeploy true` ignores not-found errors.')]
undeploy ignore_not_found="false": kustomize
    "{{KUSTOMIZE}}" build config/default | "{{KUBECTL}}" delete --ignore-not-found={{ignore_not_found}} -f -

# --------------------------------------------------------------- Dependencies

[group('dependencies')]
[doc('Download kustomize into bin/ if necessary.')]
kustomize: (_go-install "kustomize" "sigs.k8s.io/kustomize/kustomize/v5" KUSTOMIZE_VERSION)

[group('dependencies')]
[doc('Download controller-gen into bin/ if necessary.')]
controller-gen: (_go-install "controller-gen" "sigs.k8s.io/controller-tools/cmd/controller-gen" CONTROLLER_TOOLS_VERSION)

[group('dependencies')]
[doc('Download golangci-lint into bin/ if necessary.')]
golangci-lint: (_go-install "golangci-lint" "github.com/golangci/golangci-lint/v2/cmd/golangci-lint" GOLANGCI_LINT_VERSION)

[group('dependencies')]
[doc('Download setup-envtest into bin/ if necessary.')]
envtest:
    #!/usr/bin/env bash
    set -euo pipefail
    version="${ENVTEST_VERSION:-$(go list -m -f '{{{{ .Version }}' sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $2, $3}')}"
    {{JUST}} _go-install setup-envtest sigs.k8s.io/controller-runtime/tools/setup-envtest "$version"

# Prints the KUBEBUILDER_ASSETS path on stdout; everything else goes to stderr.
[group('dependencies')]
[doc('Download the envtest binaries (kube-apiserver, etcd) into bin/ and print their path.')]
setup-envtest: envtest
    #!/usr/bin/env bash
    set -euo pipefail
    k8s="${ENVTEST_K8S_VERSION:-$(go list -m -f '{{{{ .Version }}' k8s.io/api | awk -F'[v.]' '{printf "1.%d", $3}')}"
    echo "Setting up envtest binaries for Kubernetes version $k8s..." >&2
    if ! "{{ENVTEST}}" use "$k8s" --bin-dir "{{LOCALBIN}}" -p path; then
      echo "Error: Failed to set up envtest binaries for version $k8s." >&2
      exit 1
    fi

[group('dependencies')]
[doc('Download operator-sdk into bin/ unless it is already installed.')]
operator-sdk:
    #!/usr/bin/env bash
    set -euo pipefail
    target="{{LOCALBIN}}/operator-sdk"
    if [ -x "$target" ] || command -v operator-sdk >/dev/null 2>&1; then exit 0; fi
    mkdir -p "{{LOCALBIN}}"
    curl -sSLo "$target" "https://github.com/operator-framework/operator-sdk/releases/download/{{OPERATOR_SDK_VERSION}}/operator-sdk_$(go env GOOS)_$(go env GOARCH)"
    chmod +x "$target"

[group('dependencies')]
[doc('Download opm into bin/ unless it is already installed.')]
opm:
    #!/usr/bin/env bash
    set -euo pipefail
    target="{{LOCALBIN}}/opm"
    if [ -x "$target" ] || command -v opm >/dev/null 2>&1; then exit 0; fi
    mkdir -p "{{LOCALBIN}}"
    curl -sSLo "$target" "https://github.com/operator-framework/operator-registry/releases/download/{{OPM_VERSION}}/$(go env GOOS)-$(go env GOARCH)-opm"
    chmod +x "$target"

# `go install` a package into bin/<name>-<version> and link bin/<name> to it.
[private]
_go-install name package version:
    #!/usr/bin/env bash
    set -euo pipefail
    target="{{LOCALBIN}}/{{name}}"
    if [ ! -f "$target-{{version}}" ]; then
      echo "Downloading {{package}}@{{version}}" >&2
      mkdir -p "{{LOCALBIN}}"
      rm -f "$target"
      GOBIN="{{LOCALBIN}}" go install "{{package}}@{{version}}"
      mv "$target" "$target-{{version}}"
    fi
    ln -sf "$target-{{version}}" "$target"

# ------------------------------------------------------------ Bundle (OLM)

[group('bundle')]
[doc('Generate the bundle manifests and metadata, then validate them.')]
bundle: manifests kustomize operator-sdk
    #!/usr/bin/env bash
    set -euo pipefail
    sdk="{{LOCALBIN}}/operator-sdk"
    [ -x "$sdk" ] || sdk="$(command -v operator-sdk)"
    flags=(-q --overwrite --version "{{VERSION}}")
    if [ -n "{{CHANNELS}}" ]; then flags+=(--channels="{{CHANNELS}}"); fi
    if [ -n "{{DEFAULT_CHANNEL}}" ]; then flags+=(--default-channel="{{DEFAULT_CHANNEL}}"); fi
    if [ "{{USE_IMAGE_DIGESTS}}" = "true" ]; then flags+=(--use-image-digests); fi
    "$sdk" generate kustomize manifests -q
    (cd config/manager && "{{KUSTOMIZE}}" edit set image controller="{{IMG}}")
    "{{KUSTOMIZE}}" build config/manifests | "$sdk" generate bundle "${flags[@]}"
    "$sdk" bundle validate ./bundle

[group('bundle')]
[doc('Build the bundle image.')]
bundle-build:
    "{{CONTAINER_TOOL}}" build -f bundle.Dockerfile -t "{{BUNDLE_IMG}}" .

[group('bundle')]
[doc('Push the bundle image.')]
bundle-push: (docker-push BUNDLE_IMG)

[group('bundle')]
[doc('Build a catalog image from BUNDLE_IMGS with opm.')]
catalog-build: opm
    #!/usr/bin/env bash
    set -euo pipefail
    opm="{{LOCALBIN}}/opm"
    [ -x "$opm" ] || opm="$(command -v opm)"
    from_index=()
    if [ -n "{{CATALOG_BASE_IMG}}" ]; then from_index=(--from-index "{{CATALOG_BASE_IMG}}"); fi
    "$opm" index add --container-tool "{{CONTAINER_TOOL}}" --mode semver --tag "{{CATALOG_IMG}}" --bundles "{{BUNDLE_IMGS}}" ${from_index[@]+"${from_index[@]}"}

[group('bundle')]
[doc('Push the catalog image.')]
catalog-push: (docker-push CATALOG_IMG)
