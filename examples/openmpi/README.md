# Run OpenMPI Workflows with shared paths

The workflow consists of two steps.
- Step1: Get the source codes and build thebinaries
- Step2: Run the binaries


Step 1. Install Argo

```shell
helm install argo-workflows argo/argo-workflows --set server.extraArgs[0]="--auth-mode=server"
```

Step 2. Run the Workflow

```shell
rm -rf ./binaries/*
kubectl apply -f build-run.yaml
```


## Step3: Access the UI

#### from Login node

To visit the workflow frontend at http://127.0.0.1:2746/workflows/default:
```bash
kubectl port-forward service/argo-workflows-server 2746:2746
```


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
