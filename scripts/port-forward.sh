#!/bin/bash

Help()
{
   # Display Help
   echo "Access a running service from your workstation."
   echo
   echo "Syntax: port-forward [-n|p|h]"
   echo "options:"
   echo "n     Namespace of the Pod."
   echo "p     Pod name."
   echo "h     Print Help Menu."
   echo
}

while getopts ":h:n:p:" option; do
        case $option in
                h) # display Help
                        Help
                        exit;;
                n) namespace=${OPTARG};;
                p) pod=${OPTARG};;
                \?) # incorrect option
                      echo "Error: Invalid option"
                      exit;;
        esac
done

set -eu

# Access Pod from your workstation.
echo -e "Waiting for ${namespace}/${pod} to become ready ..."
kubectl wait pods -n "${namespace}" "${pod}" --for condition=Ready --timeout=120s

CONTAINER_IP=$(kubectl get endpoints "${pod}" -n "${namespace}" -o=jsonpath='{.subsets[0].addresses[0].ip}')
CONTAINER_PORT=$(kubectl get endpoints "${pod}" -n "${namespace}" -o=jsonpath='{.subsets[0].ports[0].port}')
COMPUTE_NODE=$(kubectl get pods "${pod}" -n "${namespace}" -o=jsonpath='{.metadata.annotations.pod\.hpk\/id}' | cut -d '/' -f 3 |  xargs -I {} squeue --job {} --noheader -o '%B')

echo "************** ${namespace}/${pod} ************* "
echo "Local Endpoint: http://127.0.0.1:${CONTAINER_PORT}"
echo -n "Tunneling: "
echo ssh -t "\${LOGIN_NODE}" -L ${CONTAINER_PORT}:127.0.0.1:${CONTAINER_PORT}  \
     ssh -L ${CONTAINER_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
echo "************************************************"
