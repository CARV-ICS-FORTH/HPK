#!/bin/bash

#install minio
pushd minio
kubectl delete -f hostpath-storage.yaml
helm uninstall my-minio -n minio
kubectl delete ns minio
popd

# install kubeflow operator
kubectl delete --wait=true -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.7.0"

pushd jhub
helm uninstall my-jupyter -n kubeflow
kubectl delete -f hostpath-storage.yaml
kubectl delete ns kubeflow
popd

# rm -f ./mc