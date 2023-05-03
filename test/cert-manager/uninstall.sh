#!/bin/bash

# Define namespace based on the current directory's name
export namespace=${PWD##*/}

# Uninstall cert-manager
helm uninstall cert-manager -n "${namespace}"

# Remove generated jobs
kubectl delete jobs -n "${namespace}" cert-manager-startupapicheck
