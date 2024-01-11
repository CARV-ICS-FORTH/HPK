#!/bin/bash

####### Preamble ###############
# Ensure Testing Namespace
if [[ -z "${TEST_NAMESPACE}" ]]; then
  # Define namespace based on the current directory's name
  export TEST_NAMESPACE=${PWD##*/}
fi
################################
NEW_ACCESS_KEY="ACCESS_KEY"
NEW_SECRET_KEY="SECRET_KEY"
sed -i "s/\(\"spark.hadoop.fs.s3a.access.key\":\s*\"\)[^\"]*\"/\1$NEW_ACCESS_KEY\"/g; s/\(\"spark.hadoop.fs.s3a.secret.key\":\s*\"\)[^\"]*\"/\1$NEW_SECRET_KEY\"/g" manifest-tpcds-benchmark.yaml
sed -i "s/\(\"spark.hadoop.fs.s3a.access.key\":\s*\"\)[^\"]*\"/\1$NEW_ACCESS_KEY\"/g; s/\(\"spark.hadoop.fs.s3a.secret.key\":\s*\"\)[^\"]*\"/\1$NEW_SECRET_KEY\"/g" manifest-tpcds-data-generation.yaml

# Remove Spark Application
# kubectl delete -f manifest-tpcds-data-generation.yaml -n "${TEST_NAMESPACE}"
# kubectl delete -f manifest-tpcds-benchmark.yaml -n "${TEST_NAMESPACE}"

# Remove Spark Operator
helm uninstall spark-operator --namespace "${TEST_NAMESPACE}"

# Remove namespace
kubectl delete namespace "${TEST_NAMESPACE}"