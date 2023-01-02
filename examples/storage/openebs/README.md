# OpenEBS


Install a a lite version of OpenEBS including only  Local PV (hostpath and device). 

```shell
kubectl apply -f ./operator.yaml
```

Create an example pod that persists data to OpenEBS Local PV Hostpath with following kubectl commands.

```shell
kubectl apply -f https://openebs.github.io/charts/examples/local-hostpath/local-hostpath-pvc.yaml
kubectl apply -f https://openebs.github.io/charts/examples/local-hostpath/local-hostpath-pod.yaml
```

Other examples:
https://github.com/openebs/openebs/tree/9912bb77b0e428cb1c58ce961e30c5162e791c50/k8s/demo/mongodb