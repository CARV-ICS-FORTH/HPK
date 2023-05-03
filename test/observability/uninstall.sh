#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################

# Remove pod
kubectl delete -f manifest.yaml -n "${TEST_NAMESPACE}"

# Remove Prometheus
helm uninstall prometheus --namespace "${TEST_NAMESPACE}"

# Remove Storage provisioner
(cd ../plugins/storage-provisioner && . ./uninstall.sh)

# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"