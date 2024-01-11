# Port forwarding

This tutorial describes how you can access your web interfaces from

- Login nodes
- Remote Workstations

== Login nodes

== Remote workstations

From your login node:

* To get the pod ip directly from pod:

```
CONTAINER_NAME="argo-artifacts" \
kubectl get pods ${CONTAINER_NAME} -o jsonpath="{.items[0].status.podIP}"
```

* To get the pod ip implicitly via a service:

```
SERVICE_NAME="example" \
kubectl describe service ${SERVICE_NAME} | awk /Endpoints/{'print $2'}
```

Let's up assume that it returns `10.244.5.103:9000`.
We will use this address on the next step.

From your workstation:

```
export CONTAINER_ADDR=10.244.5.103:9000
export COMPUTE_NODE=jedi1
export LOGIN_NODE=thegates
export LOCAL_PORT=18080

ssh -t ${LOGIN_NODE} -L ${LOCAL_PORT}:127.0.0.1:${LOCAL_PORT} ssh -L ${LOCAL_PORT}:${CONTAINER_ADDR} ${COMPUTE_NODE}
```

and then you can access the service from your browser `http://localhost:18080/minio/login`


# To copy data to remote node

```
scp -o ProxyJump=thegates -r  Makefile bin/ test/ jedi1:~/
```