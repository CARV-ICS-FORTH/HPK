#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}

  # Set namespace
  kubectl create namespace "${TEST_NAMESPACE}"
fi
set -eu
################################

# Update Helm Repos
helm repo add mlbench https://carv-ics-forth.github.io/frisbee/charts
helm repo update


helm install mlbench  --set master.service.type=ClusterIP --set limits.cpu=1 --set limits.gpu=0
