#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################


# Remove Imputation Application
kubectl delete -f manifest.yaml -n "${TEST_NAMESPACE}"

# Remove Argo
helm uninstall argo-workflows --namespace "${TEST_NAMESPACE}"

# Remove Minio
helm uninstall argo-artifacts --namespace "${TEST_NAMESPACE}"

# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"