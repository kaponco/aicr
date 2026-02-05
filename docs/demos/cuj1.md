# Eidos - CUJ1

> Assuming authN to any EKS cluster with 2+ H100 node (meeting recipe constraints)

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

## Generate Bundle

> Updates selectors and tolerations as needed

```shell
eidos bundle \
  --recipe recipe.yaml \
  --system-node-selector nodeGroup=system-pool \
  --accelerated-node-selector nodeGroup=gpu-worker \
  --accelerated-node-toleration nvidia.com/gpu=present:NoSchedule
```

## Install Bundle into the Cluster

```shell
cd ./bundle
helm dependency update
helm install eidos-stack . -f values.yaml
```

## Validate Cluster 

```shell
eidos validate \
  --phase readiness \ 
  --phase deployment \
  --phase conformance \
  --output recipe.yaml
```

## Run Job

> May need to migrate to [TrainJob-v2](https://www.kubeflow.org/docs/components/trainer/operator-guides/migration/#new-trainjob-v2)

```shell
kubectl apply -f https://raw.githubusercontent.com/kubeflow/training-operator/master/examples/pytorch/simple.yaml
kubectl get pytorchjobs
kubectl get pods -l training.kubeflow.org/job-name=pytorch-simple
kubectl logs -f pytorch-simple-master-0
```

## Success

Job Success + Fabric Bandwidth Range

> Synthetic workload, perf checks in CUJ2
