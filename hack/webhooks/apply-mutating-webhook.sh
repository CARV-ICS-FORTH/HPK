#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

WEBHOOK_FILEPATH=$1

echo "Replace CA_BUNDLE to File ${WEBHOOK_FILEPATH}"
echo "Virtual-Kubelet IP: ${VKUBELET_ADDRESS}"

export CLUSTER_NAME=$(kubectl config get-contexts $(kubectl config current-context) --no-headers | awk '{print $3}')
export CA_BUNDLE=$(kubectl config view --raw --flatten -o json | jq -r '.clusters[] | select(.name == "'${CLUSTER_NAME}'") | .cluster."certificate-authority-data"')

if command -v envsubst >/dev/null 2>&1; then
        envsubst < "${WEBHOOK_FILEPATH}" | kubectl apply -f -
else
        sed -e "s|\${CA_BUNDLE}|${CA_BUNDLE}|g" ${WEBHOOK_FILEPATH} | kubectl apply -f -
fi
