#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################


# Delete MLBench Runtime
helm uninstall mlbench --wait -n "${TEST_NAMESPACE}"

# Delete namespace
kubectl delete namespace "${TEST_NAMESPACE}"