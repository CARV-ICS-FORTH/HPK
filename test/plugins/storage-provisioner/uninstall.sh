#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################

# Remove provisioner and storage class
helm uninstall  storage-provisioner --namespace "${TEST_NAMESPACE}"

# Remove dataspace from nodes
export BASEPATH="${HOME}/scratch/openebs/local"
rm -rf "${BASEPATH}"


# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"