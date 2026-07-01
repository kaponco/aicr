# ADR-014: NVSentinel on OpenShift (Monitoring Only)

## Status

**Proposed** — 2026-07-01.

## Problem

NVSentinel is disabled on OCP (`ocp.yaml:88-91`, `enabled: false`). The
comment says "upstream Helm chart, no OLM variant needed," but two
blockers prevent simply flipping the flag:

1. **cert-manager dependency.** NVSentinel requires cert-manager for
   internal TLS (webhook certificates). The base overlay provides
   cert-manager as an upstream Helm chart, but the OCP overlay disablesv
   it. OCP does not ship cert-manager — it must be installed separately.

2. **OCP-specific runtime issues.** The PDF guide
   (`NVSentinel-OCP-Complete-Guide.pdf`, tested on v1.8.0 / SNO / DGX
   V100) documents four issues that required fixes:

   | # | Issue | Root Cause | Fix |
   |---|-------|-----------|-----|
   | 1 | SCC blocks pods | `restricted-v2` SCC prevents root + hostPath | ClusterRoleBinding for privileged SCC |
   | 2 | SELinux blocks socket | CoreOS SELinux prevents Unix socket creation | `seLinuxOptions: spc_t` on containers |
   | 3 | DCGM wrong namespace | Chart defaults to `gpu-operator`, OCP uses `nvidia-gpu-operator` | Values override for endpoint |
   | 4 | MongoDB init bug | Bitnami MongoDB replica set timing on OCP | Switched to Percona MongoDB Operator |

   Issue 4 is remediation-mode only and out of scope for this ADR.

## Security Posture Warning

This design results in a **compromised security posture** on OCP. The
upstream NVSentinel chart (v1.9.0) hardcodes `securityContext` fields
(e.g., `runAsUser: 0`, `privileged: true`) directly in its templates —
they are not driven by values and cannot be overridden. This forces
AICR to grant the `privileged` SCC (the weakest security posture OCP
offers) to all NVSentinel ServiceAccounts, even though only one
workload (metadata-collector) genuinely requires it. The remaining
workloads — including labeler, which could run under `restricted-v2`
with zero changes — are unnecessarily over-privileged. See the
detailed per-workload analysis in the Consequences section below.

This is an accepted trade-off to unblock GPU health monitoring on OCP
using the upstream chart as-is. An upstream issue should be filed to
request templated `securityContext` blocks, which would enable
least-privilege SCC assignment per workload.

## Scope

**Monitoring-only mode:** gpu-health-monitor, platform-connectors,
labeler. No fault-quarantine, no node-drainer, no MongoDB. This is the
NVSentinel default and covers the primary value: surfacing GPU health as
Kubernetes Node Conditions (e.g., `GpuThermalWatch: False`).

Full remediation mode (cordon + drain) is a follow-up.

## Decision

### Install cert-manager via OLM

Add a `cert-manager-ocp-olm` component following the established
two-phase OLM pattern (ADR-013). Red Hat publishes the **cert-manager
Operator for Red Hat OpenShift** in the `redhat-operators` catalog.

Unlike NFD and GPU Operator, cert-manager does not need a Phase 2 CR
chart — the operator is functional after the Subscription reaches
`Succeeded`. A single OLM component suffices.

### Enable the upstream NVSentinel Helm chart on OCP

The upstream NVSentinel Helm chart (`oci://ghcr.io/nvidia/nvsentinel`
v1.9.0) deploys on OCP using the standard Helm path — no OLM pair
needed.

**Important:** The PDF guide (`NVSentinel-OCP-Complete-Guide.pdf`) was
written against a **fork** (`github.com/ShiraEzra/NVSentinel` v1.8.0),
not the upstream chart. The `openshift.enabled: true` flag and its
SCC/SELinux fixes exist only in that fork — they are **not present** in
the upstream v1.9.0 chart. AICR must provide the three OCP fixes
itself via in-tree manifests and values overrides.

### Container images (monitoring-only mode)

All images are from `ghcr.io/nvidia` — no `docker.io` registry alias
issues. No init containers are rendered in monitoring-only mode.

| Workload | Kind | Image |
|----------|------|-------|
| gpu-health-monitor-dcgm-3.x | DaemonSet | `ghcr.io/nvidia/nvsentinel/gpu-health-monitor:v1.9.0-dcgm-3.x` |
| gpu-health-monitor-dcgm-4.x | DaemonSet | `ghcr.io/nvidia/nvsentinel/gpu-health-monitor:v1.9.0-dcgm-4.x` |
| platform-connectors | DaemonSet | `ghcr.io/nvidia/nvsentinel/platform-connectors:v1.9.0` |
| metadata-collector | DaemonSet | `ghcr.io/nvidia/nvsentinel/metadata-collector:v1.9.0` |
| syslog-health-monitor-regular | DaemonSet | `ghcr.io/nvidia/nvsentinel/syslog-health-monitor:v1.9.0` |
| syslog-health-monitor-kata | DaemonSet | `ghcr.io/nvidia/nvsentinel/syslog-health-monitor:v1.9.0` |
| labeler | Deployment | `ghcr.io/nvidia/nvsentinel/labeler:v1.9.0` |

### OCP security requirements per workload

| Workload | SA | privileged | hostNetwork | hostPID | hostPath |
|----------|----|-----------|-------------|---------|----------|
| gpu-health-monitor (both) | default | - | - | - | Yes (socket, GPU metadata) |
| platform-connectors | platform-connectors | - | - | - | Yes (socket) |
| metadata-collector | metadata-collector | Yes | Yes | Yes | Yes (GPU metadata) |
| syslog-health-monitor (both) | default | - | - | - | Yes (host logs) |
| labeler | labeler | - | - | - | - |

### OCP fix 1: SCC — ClusterRoleBinding for privileged SCC

OCP's default `restricted-v2` SCC blocks pods that require root,
hostPath, hostNetwork, or hostPID. The GPU Operator on OCP handles this
via its OLM-managed operator, which creates the necessary SCCs
internally. NVSentinel uses the upstream Helm chart directly, so AICR
must provide the SCC binding as an in-tree manifest.

The chart creates three ServiceAccounts (`labeler`,
`metadata-collector`, `platform-connectors`). The workloads using
`default` SA (gpu-health-monitor, syslog-health-monitor) also need
privileged access for hostPath volumes.

An in-tree manifest grants the `privileged` SCC to all four
ServiceAccounts:

```yaml
# recipes/components/nvsentinel/manifests/ocp-scc.yaml
{{- $v := index .Values "nvsentinel" }}
{{- $sa := list "default" "labeler" "metadata-collector" "platform-connectors" }}
{{- range $sa }}
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: nvsentinel-scc-{{ . }}
  labels:
    app.kubernetes.io/managed-by: {{ $.Release.Service }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:scc:privileged
subjects:
  - kind: ServiceAccount
    name: {{ . }}
    namespace: nvsentinel
---
{{- end }}
```

This also resolves the SELinux socket issue (PDF issue 2). CoreOS
SELinux blocks Unix socket creation at `/var/run/nvsentinel.sock`, and
the PDF fix sets `seLinuxOptions: { type: spc_t }` on affected
containers. However, the upstream chart (v1.9.0) **hardcodes** all
`securityContext` blocks in its templates — they are not driven by
values, so a values override would have no effect. The `privileged`
SCC already sets `seLinuxContext: RunAsAny`, which allows pods to run
as `spc_t` and create Unix sockets without restriction. No separate
fix is needed.

### OCP fix 2: DCGM namespace override

The GPU Operator on OCP installs into the `nvidia-gpu-operator`
namespace (set by the OLM Subscription), not `gpu-operator` (the
upstream Helm chart default). NVSentinel's gpu-health-monitor connects
to the DCGM Host Engine service, so the endpoint must be overridden:

```yaml
# In values-ocp.yaml
global:
  dcgm:
    service:
      endpoint: "nvidia-dcgm.nvidia-gpu-operator.svc"
```

## Architecture

### New components

| Component | Type | Purpose |
|-----------|------|---------|
| `cert-manager-ocp-olm` | In-tree local Helm (OLM) | Installs Red Hat cert-manager operator via OLM Subscription |

### Registry entries

```yaml
# recipes/registry.yaml (new)
- name: cert-manager-ocp-olm
  displayName: cert-manager OCP (OLM)
  valueOverrideKeys:
    - certmanagerocpolm
  healthCheck:
    assertFile: checks/cert-manager-ocp-olm/health-check.yaml
  helm:
    defaultRepository: ""
    defaultNamespace: cert-manager-operator
```

The existing `nvsentinel` registry entry is unchanged — it already
points to the upstream chart at `oci://ghcr.io/nvidia`.

### Component files

```text
recipes/components/
├── cert-manager-ocp-olm/
│   ├── values.yaml
│   ├── readiness.yaml
│   └── manifests/
│       ├── operatorgroup.yaml
│       └── subscription.yaml
├── nvsentinel/
│   ├── values.yaml               # existing base values
│   ├── values-ocp.yaml           # NEW: OCP-specific overrides
│   └── manifests/
│       ├── talos-namespace.yaml   # existing
│       └── ocp-scc.yaml          # NEW: privileged SCC bindings
```

### cert-manager-ocp-olm values

```yaml
namespace: cert-manager-operator
subscription:
  name: openshift-cert-manager-operator
  channel: stable-v1
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
operatorGroup:
  name: cert-manager-operator-group
  targetNamespaces: []
```

### NVSentinel OCP values (`values-ocp.yaml`)

```yaml
# DCGM Host Engine runs in nvidia-gpu-operator on OCP,
# not gpu-operator (issue 3 — namespace override)
global:
  dcgm:
    service:
      endpoint: "nvidia-dcgm.nvidia-gpu-operator.svc"

# networkPolicy must be disabled — the default metrics-access policy
# restricts ingress to metrics ports only, which can block cert-manager
# webhook traffic in the same namespace.
networkPolicy:
  enabled: false
```

**SELinux note:** The `privileged` SCC (granted via `ocp-scc.yaml`)
already sets `seLinuxContext: RunAsAny`, which allows `spc_t`. No
explicit `seLinuxOptions` override in values is needed when pods run
under the privileged SCC.

### Overlay changes (`recipes/overlays/ocp.yaml`)

```yaml
# Replace the current disabled entry:
#   - name: nvsentinel
#     type: Helm
#     overrides:
#       enabled: false
#
# With:

# cert-manager via OLM (required by nvsentinel for webhook TLS)
- name: cert-manager-ocp-olm
  type: Helm
  valuesFile: components/cert-manager-ocp-olm/values.yaml
  manifestFiles:
    - components/cert-manager-ocp-olm/manifests/operatorgroup.yaml
    - components/cert-manager-ocp-olm/manifests/subscription.yaml

# NVSentinel — upstream Helm chart with OCP fixes
- name: nvsentinel
  type: Helm
  valuesFile: components/nvsentinel/values-ocp.yaml
  manifestFiles:
    - components/nvsentinel/manifests/ocp-scc.yaml
  dependencyRefs:
    - cert-manager-ocp-olm
    - gpu-operator-ocp
```

The base `cert-manager` component remains `enabled: false` on OCP
(replaced by the OLM variant). The existing `prometheus-operator-crds`
disable also remains — NVSentinel's PodMonitor CRs are optional and
will fail silently without the CRDs, which is acceptable for
monitoring-only mode.

### Deployment order

```
nfd-ocp-olm → nfd-ocp → gpu-operator-ocp-olm → gpu-operator-ocp
                                                        │
cert-manager-ocp-olm ──────────────────────────────────▶│
                                                        ▼
                                                   nvsentinel
```

### Health check

The existing `checks/nvsentinel/health-check.yaml` works unchanged —
it checks the `labeler` Deployment in the `nvsentinel` namespace and
validates no pods are in bad states. The namespace is the same on OCP
as on vanilla Kubernetes.

A new `recipes/components/cert-manager-ocp-olm/readiness.yaml` gates
on the cert-manager CSV reaching `Succeeded` at deploy time (emitted
as a `-readiness` folder by the bundler when `--readiness-hooks` is
enabled). This is the same pattern used by `gpu-operator-ocp-olm`,
`nfd-ocp-olm`, and `network-operator-ocp-olm`. The registry entry
points to a no-op `checks/cert-manager-ocp-olm/health-check.yaml`
(runtime health is covered by the readiness gate).


### Security Posture — Known Limitation

The upstream NVSentinel chart (v1.9.0) **hardcodes** `securityContext`
in its templates — the fields are not driven by values and cannot be
overridden. This forces a blanket `privileged` SCC grant across all
ServiceAccounts, which is the weakest security posture OCP offers.

**Per-workload analysis:**

| Workload | Image User | Chart Forces | Actually Needs | Ideal SCC |
|----------|-----------|-------------|----------------|-----------|
| labeler | `65532` (nonroot, Chainguard distroless) | — | No elevated privileges | `restricted-v2` |
| platform-connectors | `65532` (nonroot, Chainguard distroless) | `runAsUser: 0` | hostPath (Unix socket) | `hostmount-anyuid` |
| gpu-health-monitor | root (Ubuntu + CUDA) | `runAsUser: 0` | hostPath (socket, GPU metadata) | `hostmount-anyuid` |
| syslog-health-monitor | root (Ubuntu + CUDA) | — | hostPath (host logs) | `hostmount-anyuid` |
| metadata-collector | `nvsentinel` (Ubuntu + CUDA) | `privileged: true`, `runAsUser: 0`, `hostNetwork`, `hostPID` | All of the above | `privileged` |

Only **metadata-collector** genuinely requires the `privileged` SCC.
The remaining workloads are over-privileged:

- **labeler** could run under `restricted-v2` with zero changes — its
  image is already nonroot distroless and the chart does not override
  `securityContext`.
- **platform-connectors** image is built for nonroot (`User: 65532`)
  but the chart hardcodes `runAsUser: 0`, forcing root. It only needs
  hostPath for the Unix socket — `hostmount-anyuid` would suffice.
- **gpu-health-monitor** and **syslog-health-monitor** need hostPath
  only — `hostmount-anyuid` would suffice.

Because the chart does not template these fields, AICR cannot apply
least-privilege SCCs per ServiceAccount. The blanket `privileged` SCC
is the only option without forking or patching the chart.

**Recommendation:** File an upstream NVSentinel issue requesting that
`securityContext` blocks be templated from values (or gated behind an
`openshift.enabled` flag). This would allow per-workload SCC
assignment and bring the OCP deployment in line with Red Hat's
security best practices. Until then, this is an accepted trade-off
for monitoring-only mode — the same security model the PDF guide's
fork used.

### Out of Scope (Follow-Up)

- Full remediation mode (fault-quarantine, node-drainer, MongoDB/Percona).
- Re-enabling prometheus-operator-crds / kube-prometheus-stack on OCP
  for PodMonitor support.
- syslog-health-monitor (known gap per PDF — labeler namespace
  mismatch, low priority).
- Upstream NVSentinel issue for templated `securityContext` to enable
  least-privilege SCC assignment on OCP.
