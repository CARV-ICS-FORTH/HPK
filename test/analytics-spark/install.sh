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

# Update Helm repos
helm repo add spark-operator https://googlecloudplatform.github.io/spark-on-k8s-operator
helm repo update

# Install Spark Operator
helm install \
  spark-operator spark-operator/spark-operator \
  --namespace "${TEST_NAMESPACE}" \
  --set enableWebhook=true

# Install Spark Application
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"
