#!/bin/bash
helm repo add minio https://charts.min.io/
helm repo add jupyterhub https://hub.jupyter.org/helm-chart/
helm repo update
#install openebs
export TEST_NAMESPACE=openebs
kubectl create ns ${TEST_NAMESPACE}
(cd ../../../test/plugins/storage-provisioner && . ./install.sh)

#install minio
pushd minio
kubectl create ns minio
# kubectl apply -f hostpath-storage.yaml
./install.sh
popd

# install kubeflow operator
kubectl apply --server-side --wait=true -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.9.2"

pushd jhub
kubectl create ns kubeflow
# kubectl apply -f hostpath-storage.yaml
helm upgrade --cleanup-on-fail --install my-jupyter jupyterhub/jupyterhub --namespace kubeflow --create-namespace --values values.yaml --version 3.3.8

MC_ACCESS_KEY=$(kubectl get secret myminio -n minio -o jsonpath="{.data.rootUser}" | base64 --decode)
MC_SECRET_KEY=$(kubectl get secret myminio -n minio -o jsonpath="{.data.rootPassword}" | base64 --decode)
echo "MinIO credentials: $MC_ACCESS_KEY $MC_SECRET_KEY"
popd

if [ -f "./mc" ]; then
    echo "Minio client exists."
else
    echo "Minio client doesnt exist, downloading...."
    wget https://dl.min.io/client/mc/release/linux-amd64/mc
    chmod +x mc
fi

MINIO_SERVICE_NAME="myminio"

# Get the ClusterIP using kubectl and JSONPath for precise output
ENDPOINT=$(kubectl get endpoints $MINIO_SERVICE_NAME -n minio -o jsonpath='{.subsets[0].addresses[0].ip}')

# Check if the endpoint was retrieved successfully
if [[ -z "$ENDPOINT" ]]; then
    echo "Error: Could not get the first endpoint for MinIO service"
    exit 1
fi