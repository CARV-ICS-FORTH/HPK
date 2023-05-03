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

# Install Storage Provisioner
(cd ../../plugins/storage-provisioner && . ./install.sh)

# Set pod
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"
