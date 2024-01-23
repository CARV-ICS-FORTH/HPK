#!/bin/bash
TEST_NAMESPACE=jhub
####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}

  # Set namespace
  kubectl create namespace "${TEST_NAMESPACE}"
fi
################################

ENDPOINT=proxy-public
POD=$(kubectl get pods -l app=jupyterhub -n jhub -o name | grep hub | head -n 1 | awk -F/ '{print $2}')

kubectl wait pods -n  "${TEST_NAMESPACE}" ${POD} --for condition=Ready --timeout=120s


CONTAINER_IP=$(kubectl get endpoints ${ENDPOINT} -n "${TEST_NAMESPACE}" -o=jsonpath='{.subsets[0].addresses[0].ip}')
CONTAINER_PORT=$(kubectl get endpoints ${ENDPOINT} -n "${TEST_NAMESPACE}" -o=jsonpath='{.subsets[0].ports[0].port}')
COMPUTE_NODE=$(kubectl get pods ${POD} -n "${TEST_NAMESPACE}" -o=jsonpath='{.metadata.annotations.pod\.hpk\/id}' | cut -d '/' -f 3 |  xargs -I {} squeue --job {} --noheader -o '%B')

echo "****************"
echo "Access JupyterHub from your workstation (http://127.0.0.1:${CONTAINER_PORT}):"
echo ssh -t "\${LOGIN_NODE}" -L ${CONTAINER_PORT}:127.0.0.1:${CONTAINER_PORT}  \
     ssh -L ${CONTAINER_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
echo "****************"

