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

# Update Helm repo
helm repo add openebs-localpv https://openebs.github.io/dynamic-localpv-provisioner
helm repo update

# Create dataspace on nodes for the storage class
export BASEPATH="${HOME}/scratch/openebs/local"

mkdir -p "${BASEPATH}"

# Install Provisioner and set default Storage Class
helm install \
       storage-provisioner openebs-localpv/localpv-provisioner  \
       --namespace "${TEST_NAMESPACE}"                          \
       --set openebsNDM.enabled=false                           \
       --set hostpathClass.basePath="${BASEPATH}"               \
       --set hostpathClass.isDefaultClass=true                  \
       --set deviceClass.enabled=false
