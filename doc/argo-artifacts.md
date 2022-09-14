```bash
helm repo add minio https://helm.min.io/ # official minio Helm charts
helm repo update
helm install argo-artifacts minio/minio --set service.type=LoadBalancer --set fullnameOverride=argo-artifacts


ACCESS_KEY=$(kubectl get secret argo-artifacts --namespace default -o jsonpath="{.data.accesskey}" | base64 --decode)
SECRET_KEY=$(kubectl get secret argo-artifacts --namespace default -o jsonpath="{.data.secretkey}" | base64 --decode)

# argo-artifacts-minio
data:
  artifactRepository: |
    s3:
      bucket: my-bucket
      keyFormat: prefix/in/bucket     #optional
      endpoint: my-minio-endpoint.default:9000        #AWS => s3.amazonaws.com; GCS => storage.googleapis.com
      insecure: true                  #omit for S3/GCS. Needed when minio runs without TLS
      accessKeySecret:                #omit if accessing via AWS IAM
        name: my-minio-cred
        key: accessKey
      secretKeySecret:                #omit if accessing via AWS IAM
        name: my-minio-cred
        key: secretKey
      useSDKCreds: true               #tells argo to use AWS SDK's default provider chain, enable for things like IRSA support
```