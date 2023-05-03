#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}

  # Set namespace
  kubectl create namespace "${TEST_NAMESPACE}"
fi
################################

# Update Helm repos
helm repo add minio https://charts.min.io/
helm repo add argo https://argoproj.github.io/argo-helm
helm repo update

# Install Minio
helm install --debug --wait \
  argo-artifacts minio/minio \
  --namespace "${TEST_NAMESPACE}" \
  --set resources.requests.memory=512Mi \
  --set replicas=1 \
  --set persistence.enabled=false \
  --set mode=standalone \
  --set fullnameOverride=argo-artifacts \
  --set buckets[0].name=artifacts,buckets[0].policy=none,buckets[0].purge=false

# Install Argo
helm install --debug --wait \
  argo-workflows argo/argo-workflows \
  --namespace "${TEST_NAMESPACE}" \
  --set server.extraArgs[0]="--auth-mode=server" \
  --set useDefaultArtifactRepo=true \
  --set useStaticCredentials=true \
  --set artifactRepository.s3.bucket=artifacts \
  --set artifactRepository.s3.insecure=true \
  --set artifactRepository.s3.accessKeySecret.name=argo-artifacts \
  --set artifactRepository.s3.accessKeySecret.key=rootUser \
  --set artifactRepository.s3.secretKeySecret.name=argo-artifacts \
  --set artifactRepository.s3.secretKeySecret.key=rootPassword \
  --set artifactRepository.s3.endpoint=argo-artifacts:9000

# Fix Authorization
kubectl create rolebinding default-admin \
  --namespace "${TEST_NAMESPACE}" \
  --clusterrole=cluster-admin \
  --serviceaccount="${TEST_NAMESPACE}":default


# Extract Access keys
ACCESS_KEY=$(kubectl get secret argo-artifacts -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootUser}" | base64 --decode)
SECRET_KEY=$(kubectl get secret argo-artifacts -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootPassword}" | base64 --decode)
echo "MinIO credentials: $ACCESS_KEY $SECRET_KEY"


# Install Imputation Application
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"
