#!/bin/bash

# Install cert-manager with its webhooks
# https://cert-manager.io/docs/installation/compatibility/
# https://cert-manager.io/docs/troubleshooting/webhook/
# https://medium.com/@denisstortisilva/kubernetes-eks-calico-and-custom-admission-webhooks-a2956b49bd0d
helm install   cert-manager jetstack/cert-manager   --namespace cert-manager   --create-namespace   --version v1.12.0 --set installCRDS=true  --set webhook.securePort=10260

