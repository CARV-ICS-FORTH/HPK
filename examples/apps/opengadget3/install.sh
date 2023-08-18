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
helm repo add frisbee https://carv-ics-forth.github.io/frisbee/charts
helm repo update

# Install Frisbee (if not already installed)
helm status frisbee -n frisbee ||  \
helm install --atomic frisbee frisbee/platform --wait -n frisbee \
    --set chaos-mesh.enabled=false

# Install Frisbee templates.
helm install  --atomic defaults frisbee/system --wait -n "${TEST_NAMESPACE}"

# Install Opengadget3.
helm install --atomic --wait opengadget3  ./helm -n "${TEST_NAMESPACE}"

# Run the experiment
kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"


# Access Grafana from your workstation.
echo -e "Waiting for Grafan to become ready ..."
sleep 10
kubectl wait pods -n  "${TEST_NAMESPACE}" grafana --for condition=Ready --timeout=120s

CONTAINER_IP=$(kubectl get endpoints grafana -n "${TEST_NAMESPACE}" -o=jsonpath='{.subsets[0].addresses[0].ip}')
CONTAINER_PORT=$(kubectl get endpoints grafana -n "${TEST_NAMESPACE}" -o=jsonpath='{.subsets[0].ports[0].port}')
COMPUTE_NODE=$(kubectl get pods grafana -n "${TEST_NAMESPACE}" -o=jsonpath='{.metadata.annotations.pod\.hpk\/id}' | cut -d '/' -f 3 |  xargs -I {} squeue --job {} --noheader -o '%B')

echo -e "To access Grafana from your workstation (http://127.0.0.1:${CONTAINER_PORT}):\n"
echo ssh -t "<LOGIN_NODE>" -L ${CONTAINER_PORT}:127.0.0.1:${CONTAINER_PORT}  \
     ssh -L ${CONTAINER_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}

