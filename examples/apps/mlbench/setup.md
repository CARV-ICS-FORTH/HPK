```bash

git clone https://github.com/mlbench/mlbench-helm.git
cd mlbench-helm

export NUM_NODES=1
export NUM_CPUS=1
export NUM_GPUS=0


# helm install mlbench --generate-name --set master.service.type=ClusterIP --set limits.cpu=1 --set limits.gpu=0 stable/dask
# helm install --name my-release -f values.yaml stable/dask

# helm install -f values.yaml --name my-release --set master.service.type=ClusterIP --set limits.cpu=1 --set limits.gpu=0 ./

helm template mlbench-hpk-demo . \
     --set limits.workers=${NUM_NODES-1} \
     --set limits.gpu=${NUM_GPUS} \
     --set limits.cpu=${NUM_CPUS-1} \
     --set master.service.type=ClusterIP | kubectl apply -f -

```