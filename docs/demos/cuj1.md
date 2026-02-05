# Eidos - CUJ1

> Assuming authN to any EKS cluster with 1+ H100 node (meeting recipe constraints)

## Gen Recipe

```shell
eidos recipe \
  --service eks \
  --accelerator h100 \
  --intent training \
  --os ubuntu \
  --platform pytorch \
  --output recipe.yaml
```

## Validate Recipe Constraints

```shell
eidos validate \
  --phase readiness \
  --output recipe.yaml
```

## Bundle

> Updates selectors and tolerations as needed

```shell
eidos bundle \
  --recipe recipe.yaml \
  --system-node-selector nodeGroup=system-pool \
  --accelerated-node-selector nodeGroup=gpu-worker \
  --accelerated-node-toleration nvidia.com/gpu=present:NoSchedule
```

## Install 

```shell
cd ./bundle
helm dependency update
helm install eidos-stack . -f values.yaml
```

## Validate

```shell
eidos validate \
  --phase readiness \ 
  --phase deployment \
  --phase conformance \
  --output recipe.yaml
```

> Success == PASS
