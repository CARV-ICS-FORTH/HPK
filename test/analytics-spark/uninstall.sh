#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################


# Remove Spark Application
kubectl delete -f manifest.yaml -n "${TEST_NAMESPACE}"

# Remove Spark Operator
helm uninstall spark-operator --namespace "${TEST_NAMESPACE}"

# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"