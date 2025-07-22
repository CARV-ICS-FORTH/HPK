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

# Update Helm repo
helm repo add dask https://helm.dask.org
helm repo update

helm install dask dask/dask \
  --namespace "${TEST_NAMESPACE}"
  --set image.tag=2024.1.0 \
  --set jupyter.image.tag=2024.1.0 \
  --set worker.image.tag=2024.1.0
