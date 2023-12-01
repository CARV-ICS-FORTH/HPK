# install argo workflows
kubectl create namespace argo
kubectl apply -n argo -f https://github.com/argoproj/argo-workflows/releases/download/v3.4.11/install.yaml

# install training operator of kubeflow
kubectl apply -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.5.0"
kubectl create rolebinding kubeflow-admin --clusterrole=admin --serviceaccount=argo:kubeflow -n argo
kubectl create sa argo -n kubeflow

# patch authentication
kubectl patch deployment \
  argo-server \
  --namespace argo \
  --type='json' \
  -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": [
  "server",
  "--auth-mode=server"
]}]'

# allow argo to update configmaps
kubectl patch role argo-role -n argo --type=json -p='[{"op": "add", "path": "/rules/-", "value": {"apiGroups":[""],"resources":["configmaps"],"verbs":["create","get","update"]}}]'


# run the workflow
kubectl create -f workflow.yaml
