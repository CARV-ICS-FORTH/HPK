#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################

# Remove deployment
helm uninstall  -n "${TEST_NAMESPACE}" cert-manager

# Remove generated jobs
kubectl delete jobs -n "${TEST_NAMESPACE}" cert-manager-startupapicheck

# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"