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

# Set pod
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"

# Wait for pods to become ready
kubectl wait pods -n "${TEST_NAMESPACE}" -l app=resnet-server --for condition=Ready --timeout=90s

# Verify deployment
export RESNET_IP=$(kubectl  get pods -l app=resnet-server -n "${TEST_NAMESPACE}" -o jsonpath="{.items[0].status.podIP}")

curl -v "http://${RESNET_IP}:8501/v1/models/resnet"