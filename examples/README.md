# Running applications with HPK

## Argo Workflows with artifacts stored in MinIO

```bash
helm repo add minio https://charts.min.io/
helm repo add argo https://argoproj.github.io/argo-helm
helm repo update

helm install argo-artifacts minio/minio \
  --set resources.requests.memory=512Mi \
  --set replicas=1 \
  --set persistence.enabled=false \
  --set mode=standalone \
  --set fullnameOverride=argo-artifacts \
  --set buckets[0].name=artifacts,buckets[0].policy=none,buckets[0].purge=false

helm install argo-workflows argo/argo-workflows \
  --set server.extraArgs[0]="--auth-mode=server" \
  --set useDefaultArtifactRepo=true \
  --set useStaticCredentials=true \
  --set artifactRepository.s3.bucket=artifacts \
  --set artifactRepository.s3.insecure=true \
  --set artifactRepository.s3.accessKeySecret.name=argo-artifacts \
  --set artifactRepository.s3.accessKeySecret.key=rootUser \
  --set artifactRepository.s3.secretKeySecret.name=argo-artifacts \
  --set artifactRepository.s3.secretKeySecret.key=rootPassword \
  --set artifactRepository.s3.endpoint=argo-artifacts:9000

kubectl create rolebinding default-admin --clusterrole=cluster-admin --serviceaccount=default:default
kubectl create -f https://raw.githubusercontent.com/argoproj/argo-workflows/master/examples/artifact-passing.yaml
```

To visit the workflow frontend at http://127.0.0.1:2746/workflows/default:
```bash
kubectl port-forward service/argo-workflows-server 2746:2746
```

And the artifacts repository at http://127.0.0.1:9001/buckets/artifacts/browse:
```bash
ACCESS_KEY=`kubectl get secret argo-artifacts -n default -o jsonpath="{.data.rootUser}" | base64 --decode`
SECRET_KEY=`kubectl get secret argo-artifacts -n default -o jsonpath="{.data.rootPassword}" | base64 --decode`
echo "MinIO credentials: $ACCESS_KEY $SECRET_KEY"

kubectl port-forward service/argo-artifacts-console 9001:9001
```

## Spark using the Spark Operator

```bash
helm repo add spark-operator https://googlecloudplatform.github.io/spark-on-k8s-operator
helm install spark-operator spark-operator/spark-operator \
  --set enableWebhook=true

cat <<EOF | kubectl apply -f -
apiVersion: "sparkoperator.k8s.io/v1beta2"
kind: SparkApplication
metadata:
  name: spark-pi
  namespace: default
spec:
  type: Scala
  mode: cluster
  image: "carvicsforth/spark:3.3.1"
  mainClass: org.apache.spark.examples.SparkPi
  mainApplicationFile: "local:///opt/spark/examples/jars/spark-examples_2.12-3.3.1.jar"
  sparkVersion: "3.3.1"
  restartPolicy:
    type: Never
  driver:
    serviceAccount: default
    labels:
      version: 3.3.1
  executor:
    instances: 2
    labels:
      version: 3.3.1
EOF
```

Follow the status using:
```bash
kubectl describe sparkapplication spark-pi
```

The result is show in the driver logs:
```bash
kubectl logs -f spark-pi-driver
```
