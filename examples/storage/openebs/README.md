= OpenEBS


Install a a lite version of OpenEBS including only  Local PV (hostpath and device). 

```shell
kubectl apply -f ./operator.yaml
```

Create an example pod that persists data to OpenEBS Local PV Hostpath with following kubectl commands.

```shell
kubectl apply -f https://openebs.github.io/charts/examples/local-hostpath/local-hostpath-pvc.yaml
kubectl apply -f https://openebs.github.io/charts/examples/local-hostpath/local-hostpath-pod.yaml
```