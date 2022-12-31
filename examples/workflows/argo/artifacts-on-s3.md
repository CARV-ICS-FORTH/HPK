# Argo Workflows with artifacts stored in MinIO

## Step 1: Argo + Minio Installation

You will need to have `helm` installed on your setup.

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
```

* Keep the following credentials. You will need them later to access Minio WebUI.

```bash
ACCESS_KEY=`kubectl get secret argo-artifacts -n default -o jsonpath="{.data.rootUser}" | base64 --decode`
SECRET_KEY=`kubectl get secret argo-artifacts -n default -o jsonpath="{.data.rootPassword}" | base64 --decode`
echo "MinIO credentials: $ACCESS_KEY $SECRET_KEY"
```

## Step 2: Run a workflow

```
kubectl create -f https://raw.githubusercontent.com/argoproj/argo-workflows/master/examples/artifact-passing.yaml
```

## Step3: Access UI

#### from Login node

To visit the workflow frontend at http://127.0.0.1:2746/workflows/default:

```bash
kubectl port-forward service/argo-workflows-server 2746:2746
```

To visit the artifacts repository at http://127.0.0.1:9001/buckets/artifacts/browse:

```bash
kubectl port-forward service/argo-artifacts-console 9001:9001
```

**Note:**  You will need the credentials of Step 1.

#### from Workstation

To visit the workflow frontend at http://127.0.0.1:2746/workflows/default:

```shell
# From the login node
>> kubectl get endpoints/argo-workflows-server  -o jsonpath="{.subsets[0].addresses[0].ip}"
10.244.5.177

# From the workstation
CONTAINER_IP=10.244.5.177 
CONTAINER_PORT=2746 
LOCAL_PORT=2746 
LOGIN_NODE=thegates 
COMPUTE_NODE=jedi1 
ssh -t ${LOGIN_NODE} -L ${LOCAL_PORT}:127.0.0.1:${LOCAL_PORT}  \
ssh -L ${LOCAL_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
```

To visit the artifacts repository at  http://127.0.0.1:9001/buckets/artifacts/browse:

```shell
# From the login node
>> kubectl get endpoints/argo-artifacts  -o jsonpath="{.subsets[0].addresses[0].ip}"
10.244.5.174

# From the workstation
CONTAINER_IP=10.244.5.174
CONTAINER_PORT=9001
LOCAL_PORT=9001 
LOGIN_NODE=thegates 
COMPUTE_NODE=jedi1 
ssh -t ${LOGIN_NODE} -L ${LOCAL_PORT}:127.0.0.1:${LOCAL_PORT}  \
ssh -L ${LOCAL_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
```

**Note:**  You will need the credentials of Step 1.

