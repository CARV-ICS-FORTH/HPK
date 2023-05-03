#!/bin/bash

# Define namespace based on the current directory's name
export namespace=${PWD##*/}

# Set namespace
kubectl create namespace "${namespace}"

# Add the Helm repository
helm repo add jetstack https://charts.jetstack.io

# Update the Helm repository cache
helm repo update

# Install cert-manager
helm install --debug \
  cert-manager jetstack/cert-manager \
  --namespace "${namespace}" \
  --version v1.11.0 \
  --set installCRDs=true
