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

# Add the Helm repository
helm repo add jetstack https://charts.jetstack.io

# Update the Helm repository cache
helm repo update

# reproduce robustnesss error with v1.14.4

# Install cert-manager
helm install cert-manager jetstack/cert-manager   \
    -n "${TEST_NAMESPACE}" \
    --version v1.12.0 \
    --set installCRDs=true \
    --set webhook.securePort=10260
    # --set config.metricsTLSConfig.dynamic.secretNamespace="${TEST_NAMESPACE}" \
    # --set config.metricsTLSConfig.dynamic.secretName="cert-manager-metrics-ca"