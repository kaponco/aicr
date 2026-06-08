# ADR-010: OpenShift Support via In-Tree Helm Charts

## Status

**Proposed** — 2026-06-07 (design-only; not implemented).

## Problem

AICR does not support OpenShift Container Platform (OCP). OCP operators are
installed through the Operator Lifecycle Manager (OLM) — Subscriptions,
CatalogSources, and OperatorGroups — not Helm charts. The existing bundler
emits Helm-only artifacts (upstream-helm or local-helm folders). There is no
mechanism to express an OLM install followed by a Custom Resource
configuration step within the current bundle structure.

Users deploying AICR recipes on OCP today must manually translate Helm
bundles into OLM resources, losing the validated-configuration guarantee.

## Non-Goals

- Helm-to-OLM migration tooling or automatic conversion.
- OCP-specific validator checks (separate work).
- OperatorHub integration or certified-operator publishing.
- OCP image mirroring or disconnected-install support (orthogonal to bundle
  format).
- Changes to the recipe resolution engine or overlay semantics.

## Decision

Model each OCP operator as **two in-tree local Helm charts** within a single
registry component:

1. **Phase 1 — OLM chart:** A local Helm chart whose templates contain the
   OLM resources (Namespace, OperatorGroup, Subscription, optionally
   CatalogSource). Values control channel, version, approval strategy, and
   source catalog. A `readiness.yaml` gates on the operator CSV reaching
   `Succeeded` before subsequent components deploy.

2. **Phase 2 — CR chart:** A local Helm chart whose templates contain the
   operator's Custom Resource(s) (e.g., `ClusterPolicy` for GPU Operator,
   `NicClusterPolicy` for Network Operator). Values control the CR spec.
   Applied after Phase 1 completes.

The two-phase split maps directly to the existing `pre` + primary pattern
in the localformat writer — or, more naturally, to two sibling components
in the registry with an explicit dependency.

### Bundle output

The bundler emits the standard numbered-folder structure. For a component
`gpu-operator-ocp`:

```
001-gpu-operator-ocp-olm/              # KindLocalHelm — OLM resources
    Chart.yaml
    templates/
        namespace.yaml
        operatorgroup.yaml
        subscription.yaml
    values.yaml
    install.sh
002-gpu-operator-ocp-olm-readiness/    # Readiness gate — waits for CSV Succeeded
    Chart.yaml
    templates/
        check-job.yaml
    install.sh                         # helm install --wait --wait-for-jobs
003-gpu-operator-ocp/                  # KindLocalHelm — Custom Resource
    Chart.yaml
    templates/
        clusterpolicy.yaml
    values.yaml
    install.sh
```

The readiness folder (`002-*-readiness`) is emitted automatically by the
localformat writer when `--readiness-hooks` is enabled and the component
has a `readiness.yaml`. No custom wait logic is needed.

All deployers (Helm, Argo CD, Helmfile) consume this structure unchanged —
each folder is a standard local Helm chart.

## Architecture

### Registry representation

Each OCP operator is registered as **two components** with an explicit
dependency:

```yaml
# recipes/registry.yaml
- name: gpu-operator-ocp-olm
  displayName: GPU Operator OCP (OLM)
  valueOverrideKeys: [gpuoperatorocpolm]
  helm:
    defaultRepository: ""           # in-tree local chart
    defaultNamespace: nvidia-gpu-operator

- name: gpu-operator-ocp
  displayName: GPU Operator OCP (CR)
  valueOverrideKeys: [gpuoperatorocp]
  dependencyRefs: [gpu-operator-ocp-olm]
  helm:
    defaultRepository: ""           # in-tree local chart
    defaultNamespace: nvidia-gpu-operator
```

`dependencyRefs` ensures the OLM chart is always ordered before the CR
chart in `DeploymentOrder`.

### In-tree chart layout

```
recipes/components/
├── gpu-operator-ocp-olm/
│   ├── values.yaml                # channel, source, approval defaults
│   ├── readiness.yaml             # gates on CSV reaching Succeeded
│   └── manifests/
│       ├── namespace.yaml         # Helm template: {{ .Values.namespace }}
│       ├── operatorgroup.yaml     # Helm template: {{ .Values.operatorGroup.* }}
│       └── subscription.yaml      # Helm template: {{ .Values.subscription.* }}
├── gpu-operator-ocp/
│   ├── values.yaml                # ClusterPolicy spec defaults
│   ├── values-training.yaml       # training overrides (e.g., migManager, gdrcopy)
│   └── manifests/
│       └── clusterpolicy.yaml     # Helm template: {{ .Values.spec.* }}
```

Manifests are **Helm templates** — they use `{{ .Values.xxx }}` syntax and
are rendered by Helm at install time with the merged values. The localformat
writer places them into the generated chart's `templates/` directory.

This enables the full overlay system: overlays reference overlay-specific
values files via `valuesFile` in `componentRefs`, and users can customize
at bundle time via `--set gpuoperatorocp:spec.driver.version=570.86.16`.

### Overlay structure

OCP overlays follow the existing pattern. A new `service: ocp` criteria
value is added:

```yaml
# recipes/overlays/ocp.yaml
kind: RecipeMetadata
apiVersion: aicr.nvidia.com/v1alpha1
metadata:
  name: ocp
spec:
  base: base
  criteria:
    service: ocp
  componentRefs:
    - name: gpu-operator-ocp-olm
      type: Helm
    - name: gpu-operator-ocp
      type: Helm

# recipes/overlays/ocp-training.yaml
kind: RecipeMetadata
apiVersion: aicr.nvidia.com/v1alpha1
metadata:
  name: ocp-training
spec:
  base: ocp
  criteria:
    service: ocp
    intent: training
  mixins:
    - os-rhel
  constraints:
    - name: K8s.server.version
      value: ">= 1.29"
  componentRefs:
    - name: gpu-operator-ocp
      type: Helm
      valuesFile: components/gpu-operator-ocp/values-training.yaml
```

### Deployer behavior

No deployer changes are required. Both phases emit `KindLocalHelm` folders,
which all existing deployers already handle:

| Deployer | Phase 1 (OLM) | Readiness gate | Phase 2 (CR) |
|----------|---------------|----------------|--------------|
| Helm | `helm upgrade --install` via `deploy.sh` | `--wait --wait-for-jobs` (readiness Job) | `helm upgrade --install` |
| Argo CD | `Application` CR, sync-wave N | `Application` CR, sync-wave N+1 | `Application` CR, sync-wave N+2 |
| Helmfile | Release entry | Release entry with `wait: true` | Release entry |

### OLM readiness gate

The OLM component carries a `readiness.yaml` using the same Chainsaw
assertion pattern as the existing `gpu-operator` readiness gate. The
localformat writer emits this as a `-readiness` folder between the OLM and
CR folders when `--readiness-hooks` is enabled:

```yaml
# recipes/components/gpu-operator-ocp-olm/readiness.yaml
apiVersion: chainsaw.kyverno.io/v1alpha1
kind: Test
metadata:
  name: gpu-operator-ocp-olm-readiness
spec:
  timeouts:
    assert: 30s
  steps:
    - name: csv-succeeded
      try:
        - assert:
            resource:
              apiVersion: operators.coreos.com/v1alpha1
              kind: ClusterServiceVersion
              metadata:
                namespace: nvidia-gpu-operator
              status:
                phase: Succeeded
```

This reuses the existing readiness hooks infrastructure — no new wait
mechanisms or deployer changes are needed.

### Values and template examples

**OLM values** (`recipes/components/gpu-operator-ocp-olm/values.yaml`):

```yaml
namespace: nvidia-gpu-operator
subscription:
  name: gpu-operator-certified
  channel: v24.9
  source: certified-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
operatorGroup:
  name: gpu-operator-group
  targetNamespaces: []             # empty = AllNamespaces
```

**OLM Subscription template** (`recipes/components/gpu-operator-ocp-olm/manifests/subscription.yaml`):

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: {{ .Values.subscription.name }}
  namespace: {{ .Values.namespace }}
spec:
  channel: {{ .Values.subscription.channel }}
  name: {{ .Values.subscription.name }}
  source: {{ .Values.subscription.source }}
  sourceNamespace: {{ .Values.subscription.sourceNamespace }}
  installPlanApproval: {{ .Values.subscription.installPlanApproval }}
```

**CR values** (`recipes/components/gpu-operator-ocp/values.yaml`):

CR values files should mirror the same keys and structure used in the
upstream Helm chart values for other services (e.g., EKS `values.yaml`).
This ensures consistency across services: the same knobs (`driver`,
`toolkit`, `dcgm`, `dcgmExporter`, `cdi`, `hostPaths`, etc.) appear in
OCP CR values as in EKS/AKS/GKE Helm values, even though the underlying
mechanism differs (ClusterPolicy CR fields vs. Helm chart values). This
makes overlay-specific values files (e.g., `values-training.yaml`)
portable across services where possible and keeps the `--set` override
keys familiar to users.

```yaml
name: gpu-cluster-policy
spec:
  operator:
    defaultRuntime: crio
  driver:
    enabled: true
    version: "570.86.16"
  toolkit:
    enabled: true
  devicePlugin:
    enabled: true
  dcgm:
    enabled: true
  dcgmExporter:
    enabled: true
  migManager:
    enabled: false
  nodeStatusExporter:
    enabled: true
  gfd:
    enabled: true
```

**CR template** (`recipes/components/gpu-operator-ocp/manifests/clusterpolicy.yaml`):

```yaml
apiVersion: nvidia.com/v1
kind: ClusterPolicy
metadata:
  name: {{ .Values.name }}
spec: {{ .Values.spec | toYaml | nindent 2 }}
```

**Training overlay values** (`recipes/components/gpu-operator-ocp/values-training.yaml`):

```yaml
spec:
  migManager:
    enabled: true
  gdrcopy:
    enabled: true
```

Overlays reference values files via `valuesFile` in `componentRefs`. Users
can further customize at bundle time:
`--set gpuoperatorocp:spec.driver.version=570.86.16`.

**Note:** Users who prefer raw manifests over Helm-based deployment can run
`helm template <release> <bundle-folder>` on any emitted `KindLocalHelm`
folder to produce plain Kubernetes YAML with all values resolved. This
works outside of AICR with a standard Helm installation.

## Component Matrix (Initial Scope)

| Operator | OLM Component | CR Component | CR Kind |
|----------|--------------|--------------|---------|
| GPU Operator | `gpu-operator-ocp-olm` | `gpu-operator-ocp` | `ClusterPolicy` |
| Network Operator | `network-operator-ocp-olm` | `network-operator-ocp` | `NicClusterPolicy` |
| Node Feature Discovery | `nfd-ocp-olm` | `nfd-ocp` | `NodeFeatureDiscovery` |

Additional operators (cert-manager, Prometheus) use the same two-phase
pattern. Each operator pair is self-contained — the OLM chart installs the
operator, the CR chart configures it.

## Files Changing

| File | Change |
|------|--------|
| `recipes/registry.yaml` | Add OCP component entries (OLM + CR pairs) |
| `recipes/components/gpu-operator-ocp-olm/` | New: OLM chart values + manifest templates |
| `recipes/components/gpu-operator-ocp/` | New: CR chart values + manifest templates |
| `recipes/components/network-operator-ocp-olm/` | New: OLM chart |
| `recipes/components/network-operator-ocp/` | New: CR chart |
| `recipes/components/nfd-ocp-olm/` | New: OLM chart |
| `recipes/components/nfd-ocp/` | New: CR chart |
| `recipes/overlays/ocp.yaml` | New: base OCP overlay |
| `recipes/overlays/ocp-training.yaml` | New: OCP training overlay |
| `recipes/overlays/ocp-inference.yaml` | New: OCP inference overlay |
| `recipes/mixins/os-rhel.yaml` | New: RHEL OS constraints mixin |
| `pkg/recipe/criteria.go` | Add `CriteriaServiceOCP` constant |
| `api/aicr/v1/server.yaml` | Add `ocp` to service enum |
| `docs/user/cli-reference.md` | Document `ocp` service value |

### Not changing

- `pkg/bundler/deployer/` — all deployers already handle `KindLocalHelm`.
- `pkg/bundler/deployer/localformat/writer.go` — existing folder numbering
  and `KindLocalHelm` generation covers the OCP case.
- `pkg/bundler/bundler.go` — no new deployer type needed.

## Testing Strategy

| Layer | Coverage |
|-------|----------|
| Unit (Go) | `pkg/recipe`: overlay resolution for `service: ocp` criteria. Table-driven tests for OCP overlay chain. |
| Chainsaw (CLI) | New `tests/chainsaw/cli/bundle-ocp/`: generate OCP recipe, bundle, verify folder structure + manifest content. |
| KWOK | Add `ocp-training` to recipe set once overlays exist. OLM CRDs are not present in KWOK — test validates bundle structure, not OLM reconciliation. |

## Acceptance Criteria

1. `aicr recipe --service ocp --accelerator h100 --intent training --os rhel`
   produces a recipe with OLM + CR component pairs.
2. `aicr bundle -r recipe.yaml -o ./bundles` emits numbered folders with
   valid Helm charts for both OLM and CR phases.
3. `aicr bundle -r recipe.yaml --deployer argocd -o ./bundles` emits Argo CD
   Applications with correct sync-wave ordering (OLM before CR).
4. `make qualify` passes.
5. Chainsaw tests verify folder structure and manifest content for OCP
   bundles.

## References

- [OLM Architecture](https://olm.operatorframework.io/docs/concepts/olm-architecture/)
- Existing two-phase pattern: `KindUpstreamHelm` + `-post` `KindLocalHelm`
  in `pkg/bundler/deployer/localformat/writer.go`
- Component dependency ordering: `dependencyRefs` in `recipes/registry.yaml`
- Overlay composition: [ADR-005](005-overlay-refactoring.md)
