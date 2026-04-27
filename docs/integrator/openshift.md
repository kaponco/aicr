# OpenShift Deployment

Deploy NVIDIA GPU and networking components on Red Hat OpenShift using the Operator Lifecycle Manager (OLM).

## Overview

The **OpenShift deployment** process follows a two-stage lifecycle:

1. **Operator-Based Installation:** Components setup is managed via Operator Lifecycle Manager (OLM) instead of standard Helm charts.

2. **CR-Driven Configuration:** Post-installation, the operator’s behavior is defined by applying targeted Custom Resources (CRs).  


### OLM Architecture:

The Operator Lifecycle Manager provides a declarative approach to managing operator lifecycles:

- **CatalogSource**: Defines the operator registry (Red Hat Certified Operators, Community Operators, etc.)
- **Subscription**: Requests installation of an operator from a catalog
- **InstallPlan**: Automatic approval/installation of operator resources (CSV, CRDs, etc.)
- **ClusterServiceVersion (CSV)**: Describes the operator and its resource requirements
- **Custom Resources (CRs)**: User-defined configurations that the operator reconciles

**Subscription vs. CR Deployment**:

The Subscription and the Custom Resource (CR) follow independent lifecycles: the Subscription bootstraps the operator environment
while the CR acts as the ongoing trigger for the operator to provision and manage the workload.
This distinction creates two separate operational flows — one for the operator instalation (i.e. subscription) and one for the workload deployment.

**OpenShift-Specific Constraints:**

- **Certified Operators**: Use Red Hat-certified operator catalogs when available
- **Security Context Constraints (SCC)**: Operators may require privileged access for driver installation
- **Entitlement**: RHEL-based driver builds may require Red Hat entitlement ConfigMaps
- **Version Alignment**: Operator versions must align with OpenShift Container Platform (OCP) version

**Typical Workflow:**

1. Generate recipe → AICR detects OpenShift and selects OLM-based components
2. Bundle generation → Creates OLM Subscription manifests and CR templates
3. Apply subscriptions → Operators install via OLM
4. Create a snapshot → Records cluster's status
5. Wait for CSV ready → Operator controllers become active
6. Apply Custom Resources → Operators reconcile GPU/network components
7. Validate deployment → Verify operator status and component readiness

## Complete Deployment Workflow

This section demonstrates the end-to-end deployment process on OpenShift with commands and expected outputs.

### 1. Generate Recipe

Generate a recipe by specifying OpenShift as the service:

```bash
aicr recipe \
  --service ocp \
  --accelerator l40 \
  --os rhel \
  --intent inference \
  --output recipe.yaml
```

**Expected Output:**

```
[cli] building recipe from criteria: criteria=criteria(service=ocp, accelerator=l40, intent=inference, os=rhel)
[cli] recipe generation completed: output=recipe.yaml 
```

**Verify Recipe Contents:**

```bash
cat recipe.yaml
```

The recipe will include OpenShift-specific component references with OLM deployment

```yaml
...
  - name: gpu-operator
    namespace: gpu-operator
    type: OLM
    dependencyRefs:
      - nfd
    manifestFiles:
      - components/gpu-operator/manifests/dcgm-exporter.yaml
    installFiles:
      - components/gpu-operator/olm/install.yaml
    resourcesFile: components/gpu-operator/resources/resources-ocp.yaml
    kinds:
      - ClusterPolicy
    customResources:
      - components/gpu-operator/resources/resources-ocp.yaml
...
```

### 2. Generate Bundle

Create deployment bundle from the recipe:

```bash
aicr bundle \
  --recipe recipe.yaml \
  --output ./ocp-bundle
```

**Expected Output:**

```
[cli] generating bundle: deployer=helm type=Helm per-component bundle recipe=/tmp/aicr-demo/recipe.yaml output=/tmp/aicr-demo/bundles oci=false
[cli] bundle generated: type=Helm per-component bundle files=16 size_bytes=68044 duration_sec=0.005469625 output_dir=/tmp/aicr-demo/bundles

Helm per-component bundle generated successfully!
Output directory: /tmp/aicr-demo/bundles
Files generated: 16
```

**Bundle Directory Structure:**

```bash
tree ocp-bundle
```

```
ocp-bundle/
├── subscribe.sh                # Step 1: Install OLM subscriptions
├── deploy.sh                   # Step 2: Deploy custom resources
├── undeploy.sh                 # Cleanup script
├── unsubscribe.sh              # OLM cleanup script
├── gpu-operator/
│   ├── install.yaml       # Subscription + OperatorGroup
│   ├── resources.yaml         # ClusterPolicy CR
│   └── README.md
└── network-operator/
    ├── install.yaml       # Subscription + OperatorGroup
    ├── resources.yaml         # NicClusterPolicy CR
    └── README.md
```

### 3. Deploy OLM Subscriptions

The `subscribe.sh` script creates namespaces and installs operator subscriptions:

```bash
cd ocp-bundle
./subscribe.sh
```

**Expected Output:**

```
==> Installing OLM Components
...
```

### 4. Wait for Operator Readiness

After installing subscriptions, wait for the ClusterServiceVersion (CSV) resources to reach `Succeeded` phase:

**Example - GPU Operator:**

```bash
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded \
  csv -n gpu-operator --all --timeout=5m
```

**Expected Output:**

```
clusterserviceversion.operators.coreos.com/gpu-operator-certified.v25.10.1 condition met
```


### 5. Deploy Custom Resources

Once operators are ready, deploy the Custom Resources that configure their behavior:

```bash
./deploy.sh
```

**Expected Output:**

```
Running pre-flight checks...
Pre-flight checks passed.
Deploying Cloud Native Stack components...
Installing nfd (openshift-nfd) via OLM custom resources...
...
Deployment complete.
...
```

**Monitor Component Rollout:**

```bash
# GPU Operator DaemonSets
watch kubectl get pods -n gpu-operator

```
**Expected Output:**
Pods should be in Runnning/Completed status


### 6. Capture Snapshot

After deployment, capture a snapshot of the cluster state for validation or record-keeping:

```bash
aicr snapshot --output snapshot.yaml
```

**Expected Output:**

```
[cli] deploying agent: namespace=default
[cli] agent deployed successfully
[cli] waiting for Job completion: job=aicr timeout=5m0s
[cli] job completed successfully
[cli] cleanup failed - resources may remain in cluster: error=
[cli] snapshot saved to file: path=snapshot.yaml
```

**Snapshot Contents:**

```yaml
apiVersion: aicr.nvidia.com/v1alpha1
kind: Snapshot
metadata:
  timestamp: "2026-04-26T10:30:00Z"
  clusterName: ocp-production
spec:
  kubernetes:
    version: v1.33.2
    platform: openshift
  gpu:
    devices:
      - model: NVIDIA H100 80GB HBM3
        count: 8
        driver: 570.86.16
  # ... additional measurements
```

### 7. Validate Deployment

Validate the deployed components against the recipe and snapshot:

```bash
aicr validate \
  --recipe recipe.yaml \
  --snapshot snapshot.yaml
```

**Expected Output:**

```
[cli] loading recipe: uri=recipe.yaml
[cli] loading snapshot: uri=snapshot.yaml
[cli] running validation: phases=[deployment]
[cli] running validation phases: runID=20260426-140901-4bd0bb84477d9a70 phases=[deployment]
[cli] readiness pre-flight: constraints=4
[cli] readiness constraint passed: name=K8s.crd.clusterpolicies.nvidia.com.established expected=true actual=true
[cli] readiness constraint passed: name=K8s.crd.nicclusterpolicies.mellanox.com.established expected=true actual=true
[cli] readiness constraint passed: name=K8s.crd.nodefeaturediscoveries.nfd.openshift.io.established expected=true actual=true
[cli] readiness constraint passed: name=K8s.server.version expected=>= 1.30 actual=v1.31.14
[cli] running validation phase: phase=deployment catalog=4 selected=2
[cli] running validator: name=operator-health phase=deployment
[cli] validator completed: name=operator-health status=failed
[cli] running validator: name=expected-resources phase=deployment
[cli] validator completed: name=expected-resources status=failed
[cli] phase completed: phase=deployment status=failed validators=2 passed=0 failed=2 duration=9.394830125s
[cli] all phases completed: runID=20260426-140901-4bd0bb84477d9a70 phases=1

=== Validation Results ===

[cli] phase result: phase=deployment status=passed duration=9.532019667s
```

