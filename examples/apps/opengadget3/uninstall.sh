#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################

# Delete the experiment
kubectl delete -f manifest.yaml -n "${TEST_NAMESPACE}"

# Delete Frisbee Runtime
helm uninstall opengadget3 --wait -n "${TEST_NAMESPACE}"

# Delete namespace
kubectl delete namespace "${TEST_NAMESPACE}"