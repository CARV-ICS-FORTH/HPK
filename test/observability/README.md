# Observability



## Install Prometheus and Grafana



To install Prometheus:

```shell
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
helm install prometheus prometheus-community/prometheus
```



To install Grafana:

```shell
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update
helm install grafana grafana/grafana
```



## Access Web UI



#### Prometheus

```shell
# From the login node
>> kubectl get endpoints/prometheus-server  -o jsonpath="{.subsets[0].addresses[0].ip}"
10.244.5.23

# From the workstation
CONTAINER_IP=10.244.5.23
CONTAINER_PORT=9090
LOCAL_PORT=9090
LOGIN_NODE=thegates
COMPUTE_NODE=jedi1
ssh -t ${LOGIN_NODE} -L ${LOCAL_PORT}:127.0.0.1:${LOCAL_PORT}  \
ssh -L ${LOCAL_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
```

You can now visit Prometheus at: http://127.0.0.1:9090



#### Grafana



```shell
# From the login node
>> kubectl get endpoints/grafana  -o jsonpath="{.subsets[0].addresses[0].ip}"
10.244.5.24

# From the workstation
CONTAINER_IP=10.244.5.24
CONTAINER_PORT=3000
LOCAL_PORT=3000
LOGIN_NODE=thegates
COMPUTE_NODE=jedi1
ssh -t ${LOGIN_NODE} -L ${LOCAL_PORT}:127.0.0.1:${LOCAL_PORT}  \
ssh -L ${LOCAL_PORT}:${CONTAINER_IP}:${CONTAINER_PORT} ${COMPUTE_NODE}
```



Then, you can visit Grafana at  http://127.0.0.1:3000

You will be asked for `username` and `password`.

Use the `admin` username and get the password using the following command:

```shell
# From the login Node
>> kubectl get secret --namespace default grafana -o jsonpath="{.data.admin-password}" | base64 --decode ; echo
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




Add Monitoring Sidecar