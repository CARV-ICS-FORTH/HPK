TEST_NAMESPACE=minio

# Install Minio
helm install --debug --wait \
  my-minio minio/minio \
  --namespace "${TEST_NAMESPACE}" \
  --set resources.requests.memory=512Mi \
  --set replicas=1 \
  --set persistence.enabled=false \
  --set mode=standalone \
  --set fullnameOverride=myminio
# Extract Minio Credentials
ACCESS_KEY=$(kubectl get secret myminio -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootUser}" | base64 --decode)
SECRET_KEY=$(kubectl get secret myminio -n "${TEST_NAMESPACE}" -o jsonpath="{.data.rootPassword}" | base64 --decode)
echo "MinIO credentials: $ACCESS_KEY $SECRET_KEY"
