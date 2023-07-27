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

# Create testing dir with a populated file.
mkdir -p /tmp/sea/
echo "Yaar ! I 'm a pirate file hijacking your tmpfs" >> /tmp/sea/pirate
echo "Sir, I 'm a privateer hunting down your pirate file" >> /tmp/sea/privateer

# Set pod
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"
