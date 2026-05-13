# OpenShift Deployment

Deploy NVIDIA GPU and networking components on Red Hat OpenShift Container Platform (OCP) using the Operator Lifecycle Manager (OLM).

## Overview

AICR supports OpenShift through a **Direct deployment type** that leverages OpenShift's native Operator Lifecycle Manager (OLM) instead of Helm charts. This approach aligns with OpenShift best practices and provides proper operator lifecycle management through the OpenShift Console.

### Why OLM Instead of Helm?

While OpenShift supports Helm installations, **OLM is the recommended and preferred installation mechanism** for operators on OpenShift because:

- **Native Integration** — Operators appear in the OpenShift Console with full lifecycle visibility
- **Automatic Updates** — OLM handles operator updates through subscription channels
- **Dependency Management** — OLM resolves operator dependencies and installation ordering
- **Security & RBAC** — Proper integration with OpenShift's security model and ClusterServiceVersions (CSVs)

### Key Concepts

**Direct Deployment Type**

The `Direct` deployment type is specifically designed for OpenShift OLM integration:
- Static YAML manifests embedded in AICR (no external chart repos or git dependencies)
- Simple `kubectl apply` deployment model with generated install scripts
- Automatic CSV wait logic for OLM components
- Works alongside `Helm` and `Kustomize` deployment types for other components

**Dual-Component Pattern**

Each OpenShift operator requires **two separate AICR components**:

1. **Operator Component** (`<name>-olm`) — Installs the operator itself via OLM
   - Contains: `Subscription`, `OperatorGroup`, optionally `CatalogSource`
   - Uses `Direct` deployment type with `olm: true` flag
   - Example: `gpu-operator-olm`, `nfd-olm`

2. **CR Component** (`<name>`) — Configures the operator's behavior
   - Contains: Custom Resources (e.g., `ClusterPolicy` for GPU Operator)
   - Uses `Direct` deployment type
   - Depends on the corresponding `-olm` component
   - Example: `gpu-operator`, `nfd`

This separation ensures the operator is fully installed and ready before applying its configuration.

### OLM Resource Flow

```text
User creates Subscription
        ↓
OLM creates ClusterServiceVersion (CSV)
        ↓
CSV deploys operator Deployment/DaemonSet
        ↓
Operator becomes ready (CSV phase: Succeeded)
        ↓
User applies Custom Resources
        ↓
Operator reconciles workload
```

## Complete Deployment Workflow

This section demonstrates the end-to-end deployment process on OpenShift.

### Prerequisites

- OpenShift Container Platform 4.14+ (Kubernetes 1.27+)
- `kubectl` or `oc` CLI configured with cluster access
- Cluster-admin privileges for operator installation
- GPU nodes properly labeled (if deploying GPU operators)

### 1. Generate Recipe

Generate a recipe for OpenShift by specifying `--service ocp`:

```bash
aicr recipe \
  --service ocp \
  --accelerator h100 \
  --os rhel \
  --intent training \
  --output recipe.yaml
```

**Expected Output:**

```text
[cli] building recipe from criteria: criteria=criteria(service=ocp, accelerator=h100, intent=training, os=rhel)
[cli] recipe generation completed: output=recipe.yaml components=4 overlays=3
```

**Verify Recipe Contents:**

```bash
cat recipe.yaml
```

The recipe will reference OpenShift-specific components using the `Direct` deployment type:

```yaml
apiVersion: aicr.nvidia.com/v1alpha1
kind: RecipeMetadata
metadata:
  name: ocp-h100-rhel-training-recipe
  
spec:
  components:
    # NFD Operator installation via OLM
    - name: nfd-olm
      type: Direct
      namespace: openshift-nfd
      sourceFile: recipes/components/nfd-olm/direct/olm.yaml
    
    # NFD Custom Resource configuration
    - name: nfd
      type: Direct
      namespace: openshift-nfd
      sourceFile: recipes/components/nfd/direct/nodefeaturediscovery.yaml
      dependencyRefs:
        - nfd-olm
    
    # GPU Operator installation via OLM
    - name: gpu-operator-olm
      type: Direct
      namespace: nvidia-gpu-operator
      sourceFile: recipes/components/gpu-operator-olm/direct/olm.yaml
      dependencyRefs:
        - nfd-olm
    
    # GPU Operator Custom Resource configuration
    - name: gpu-operator
      type: Direct
      namespace: nvidia-gpu-operator
      sourceFile: recipes/components/gpu-operator/direct/clusterpolicy.yaml
      dependencyRefs:
        - gpu-operator-olm
        - nfd
```

### 2. Generate Bundle

Create deployment bundle from the recipe:

```bash
aicr bundle \
  --recipe recipe.yaml \
  --output ./ocp-bundle
```

**Expected Output:**

```text
[cli] generating bundle: deployer=helm type=Helm per-component bundle recipe=recipe.yaml output=./ocp-bundle
[cli] bundle generated: type=Helm per-component bundle files=12 size_bytes=45120 output_dir=./ocp-bundle
```

**Bundle Directory Structure:**

```bash
tree ocp-bundle
```

```text
ocp-bundle/
├── deploy.sh                 # Orchestration script - deploys all components in order
├── undeploy.sh               # Orchestration script - removes all components
├── 001-nfd-olm/
│   ├── olm.yaml              # Subscription + OperatorGroup manifests
│   ├── install.sh            # Generated install script with CSV wait
│   └── uninstall.sh          # Generated cleanup script
├── 002-nfd/
│   ├── nodefeaturediscovery.yaml  # NodeFeatureDiscovery CR
│   ├── install.sh            # Generated install script
│   └── uninstall.sh          # Generated cleanup script
├── 003-gpu-operator-olm/
│   ├── olm.yaml              # Subscription + OperatorGroup manifests
│   ├── install.sh            # Generated install script with CSV wait
│   └── uninstall.sh          # Generated cleanup script
├── 004-gpu-operator/
│   ├── clusterpolicy.yaml    # ClusterPolicy CR
│   ├── install.sh            # Generated install script
│   └── uninstall.sh          # Generated cleanup script
├── recipe.yaml               # Recipe used to generate bundle
├── checksums.txt             # SHA256 checksums for verification
└── README.md                 # Bundle documentation
```

### 3. Deploy an OLM Component

Deploy operators through OLM subscriptions. The install scripts automatically create namespaces and wait for operator readiness.

**Deploy NFD Operator:**

```bash
cd ocp-bundle
./001-nfd-olm/install.sh
```

**Expected Output:**

```text
namespace/openshift-nfd created
operatorgroup.operators.coreos.com/openshift-nfd-operatorgroup created
subscription.operators.coreos.com/nfd created
Waiting for OLM operator CSV to reach Succeeded phase...
CSV phase: Installing, waiting... (5s/300s)
CSV phase: Installing, waiting... (10s/300s)
CSV phase: Succeeded
CSV reached Succeeded phase
```

The script automatically polls for CSV readiness with a 5-minute timeout.


### 4. Verify Operator Installation

Check that ClusterServiceVersions are in `Succeeded` phase:

```bash
kubectl get csv -n openshift-nfd
```

**Expected Output:**

```text
NAME                                    DISPLAY                         VERSION   REPLACES   PHASE
nfd.v4.16.0-202601091828                Node Feature Discovery Operator 4.16.0               Succeeded
```

**Check Operator Pods:**

```bash
kubectl get pods -n openshift-nfd
```

Operator pods should be in `Running` status.

### 5. Deploy Custom Resources

Once operators are ready, deploy the Custom Resources that configure their behavior:

**Deploy NFD Custom Resource:**

```bash
./002-nfd/install.sh
```

**Expected Output:**

```text
nodefeaturediscovery.nfd.openshift.io/nfd-instance created
```

**Deploy GPU Operator ClusterPolicy:**

```bash
./004-gpu-operator/install.sh
```

**Expected Output:**

```text
clusterpolicy.nvidia.com/cluster-policy created
```

### 6. Monitor Component Rollout

Watch the operators and DaemonSets as they deploy across nodes:

**Check NFD Pods:**

```bash
kubectl get pods -n openshift-nfd
```

```text
NAME                                      READY   STATUS    RESTARTS   AGE
nfd-controller-manager-7846858557-x6qn4   1/1     Running   0          5m1s
nfd-gc-5f975dd6d4-lrnxc                   1/1     Running   0          4m21s
nfd-master-d5f49b9b7-hl9t8                1/1     Running   0          4m21s
nfd-worker-68rnq                          1/1     Running   0          4m21s
nfd-worker-jh6k8                          1/1     Running   0          4m21s
nfd-worker-qj8zn                          1/1     Running   0          4m21s
```



### 7. Capture Snapshot

After deployment, capture a snapshot of the cluster state for validation:

```bash
aicr snapshot --output snapshot.yaml
```

**Expected Output:**

```text
[cli] deploying agent: namespace=default
[cli] agent deployed successfully
[cli] waiting for Job completion: job=aicr-snapshot timeout=5m0s
[cli] job completed successfully
[cli] snapshot saved to file: path=snapshot.yaml
```

### 8. Validate Deployment

Validate the deployed components against the recipe and snapshot:

```bash
aicr validate \
  --recipe recipe.yaml \
  --snapshot snapshot.yaml
```

**Expected Output:**

```text
[cli] loading recipe: uri=recipe.yaml
[cli] loading snapshot: uri=snapshot.yaml
[cli] running validation: phases=[deployment]
[cli] readiness pre-flight: constraints=3
[cli] readiness constraint passed: name=K8s.crd.clusterpolicies.nvidia.com.established expected=true actual=true
[cli] readiness constraint passed: name=K8s.crd.nodefeaturediscoveries.nfd.openshift.io.established expected=true actual=true
[cli] readiness constraint passed: name=K8s.server.version expected=>= 1.27 actual=v1.27.4
[cli] phase completed: phase=deployment status=passed validators=2 passed=2 failed=0
[cli] all phases completed: phases=1

=== Validation Results ===
[cli] phase result: phase=deployment status=passed duration=8.2s
```

## Automated Deployment with deploy.sh

As an alternative to running individual component install scripts, the bundle includes top-level orchestration scripts that deploy all components in the correct dependency order.

**Deploy all components:**

```bash
cd ocp-bundle
./deploy.sh
```

The `deploy.sh` script:
- Runs pre-flight checks (validates cluster state, checks for stale resources)
- Loops through all `NNN-*/` directories in order
- Executes each component's `install.sh` script
- Waits for OLM operators to reach CSV `Succeeded` phase (for components with `olm: true`)
- Provides retry logic with exponential backoff on failures
- Continues asynchronously for operator-based components

**Supported flags:**

| Flag | Description |
|------|-------------|
| `--no-wait` | Skip Helm/kubectl wait (applies manifests but doesn't block on readiness) |
| `--best-effort` | Continue past individual component failures instead of exiting |
| `--retries N` | Retry failed operations N times with backoff (default: 5) |

**Example with options:**

```bash
./deploy.sh --best-effort --retries 3
```

**Undeploy all components:**

```bash
./undeploy.sh
```

The `undeploy.sh` script:
- Removes components in reverse dependency order (OLM CRs before operators)
- Deletes ClusterServiceVersions before Subscriptions
- Optionally preserves namespaces and PVCs

**Undeploy flags:**

| Flag | Description |
|------|-------------|
| `--keep-namespaces` | Preserve namespaces after removing components |
| `--delete-pvcs` | Delete PersistentVolumeClaims (default: preserve) |
| `--timeout SECONDS` | Timeout for kubectl/helm operations (default: 120s) |
| `--skip-preflight` | Skip pre-flight finalizer checks |

**Example:**

```bash
./undeploy.sh --keep-namespaces --timeout 300
```

These orchestration scripts simplify deployment but the individual component `install.sh`/`uninstall.sh` scripts provide more granular control when needed.

## OpenShift Console Integration

After deploying via OLM, operators are visible in the OpenShift Console:

**View Installed Operators:**
1. Navigate to **Operators → Installed Operators**
2. Select namespace: `nvidia-gpu-operator` or `openshift-nfd`
3. View operator details, status, and managed resources

**View Custom Resources:**
1. Click on the installed operator
2. Navigate to the **ClusterPolicy** or **NodeFeatureDiscovery** tab
3. View CR details and status

This provides a GUI alternative to `kubectl` for monitoring operator health.

## Cleanup

**Option 1: Automated cleanup (recommended)**

Use the top-level `undeploy.sh` script to remove all components in reverse dependency order:

```bash
cd ocp-bundle
./undeploy.sh
```

This automatically removes CRs before operators and CSVs before Subscriptions.

**Option 2: Manual cleanup**

Remove individual components using their uninstall scripts in reverse dependency order:

```bash
# Remove Custom Resources first
./004-gpu-operator/uninstall.sh
./002-nfd/uninstall.sh

# Remove Operators
./003-gpu-operator-olm/uninstall.sh
./001-nfd-olm/uninstall.sh
```

The uninstall scripts automatically remove ClusterServiceVersions before deleting Subscriptions to ensure clean operator removal.

## Understanding the Install Scripts

### OLM Component Install Script

For components with `olm: true`, the generated `install.sh` includes:

1. **Namespace Creation** — Creates the operator namespace if it doesn't exist
2. **Manifest Application** — Applies the OLM resources (Subscription, OperatorGroup)
3. **CSV Wait Logic** — Polls for ClusterServiceVersion to reach `Succeeded` phase
   - Timeout: 300 seconds (5 minutes)
   - Poll interval: 5 seconds
   - Exits with code 0 on success, 1 on timeout/failure

**Example script excerpt:**

```bash
# Create namespace
kubectl create namespace nvidia-gpu-operator 2>/dev/null || true

# Apply OLM resources
kubectl apply -f olm.yaml -n nvidia-gpu-operator

# Wait for CSV
echo "Waiting for OLM operator CSV to reach Succeeded phase..."
TIMEOUT=300
while [ $ELAPSED -lt $TIMEOUT ]; do
  CSV_PHASE=$(kubectl get csv -n nvidia-gpu-operator -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
  if [ "$CSV_PHASE" = "Succeeded" ]; then
    echo "CSV reached Succeeded phase"
    exit 0
  fi
  sleep 5
  ELAPSED=$((ELAPSED + 5))
done
echo "ERROR: Timeout waiting for CSV"
exit 1
```

### CR Component Install Script

For Custom Resource components (without `olm: true`):

1. **Namespace Creation** — Creates the namespace if specified
2. **Manifest Application** — Applies the Custom Resource
3. **No Wait Logic** — Returns immediately after apply

The operator installed via OLM handles reconciliation asynchronously.


## References

- [NVIDIA GPU Operator on OpenShift](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/install-gpu-ocp.html)
- [Understanding OLM](https://docs.redhat.com/en/documentation/openshift_container_platform/4.2/html/operators/understanding-the-operator-lifecycle-manager-olm)
- [ADR-007: OpenShift Integration](../design/007-openshift-integration.md)
