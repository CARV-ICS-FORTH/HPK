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
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install Storage Provisioner (directory changed in the subshell)
(cd ../plugins/storage-provisioner && . ./install.sh)

# Install Prometheus
helm install --debug \
  prometheus prometheus-community/prometheus  \
  --namespace "${TEST_NAMESPACE}"

# Set pod
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"


# Verify Deployment