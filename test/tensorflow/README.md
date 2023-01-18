Deploys a pod and replies with Tensorflow Service a model.

To run it

```shell
kubectl apply -f resnet.yaml
```

To verify it:

```shell
kubectl  get pods -l app=resnet-server -o jsonpath="{.items[0].status.podIP}"

RESNET_IP=$(kubectl  get pods -l app=resnet-server -o jsonpath="{.items[0].status.podIP}"); \
curl -v http://${RESNET_IP}:8501/v1/models/resnet
```