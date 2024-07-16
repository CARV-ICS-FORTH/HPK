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

# Update Helm repos
helm repo add spark-operator https://kubeflow.github.io/spark-operator
helm repo add minio https://charts.min.io/
helm repo update

# Install Minio
helm install --debug --wait \
  argo-artifacts minio/minio \
  --namespace "${TEST_NAMESPACE}" \
  --set resources.requests.memory=512Mi \
  --set replicas=1 \
  --set persistence.enabled=false \
  --set mode=standalone \
  --set fullnameOverride=artifacts \
  --set buckets[0].name=spark-k8s-data,buckets[0].policy=none,buckets[0].purge=false

# Extract Minio Credentials
ACCESS_KEY=$(kubectl get secret artifacts -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootUser}" | base64 --decode)
SECRET_KEY=$(kubectl get secret artifacts -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootPassword}" | base64 --decode)
echo "MinIO credentials: $ACCESS_KEY $SECRET_KEY"


# Install Spark Operator
# https://github.com/GoogleCloudPlatform/spark-on-k8s-operator/tree/master/charts/spark-operator-chart
helm install \
  spark-operator spark-operator/spark-operator \
  --namespace "${TEST_NAMESPACE}" \
  --set sparkJobNamespace="${TEST_NAMESPACE}" \
  --set serviceAccounts.spark.name="spark" \
  --set enableWebhook=true \
  --set image.tag=v1beta2-1.4.5-3.5.0

# Install Spark Application
#kubectl apply -f manifest.yaml -n "${TEST_NAMESPACE}"
sed -i "s/ACCESS_KEY/$ACCESS_KEY/g; s/SECRET_KEY/$SECRET_KEY/g" manifest-tpcds-benchmark.yaml
sed -i "s/ACCESS_KEY/$ACCESS_KEY/g; s/SECRET_KEY/$SECRET_KEY/g" manifest-tpcds-data-generation.yaml