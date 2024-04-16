## Notes

1. minikube start --memory='4000' --cpus='4' --disk-size='50000mb' --driver=kvm2 --nodes 3    
2. deploy nfs
3. install minio
   1. kubectl create ns minio  
   2. kubectl apply -f storage.yaml
   3. ./install.sh
4. kubectl apply --wait=true -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.7.0" # install kubeflow
5. install jhub
   1. kubectl create ns kubeflow
   2. kubectl apply -f storage.yaml
   3. helm upgrade --cleanup-on-fail --install my-jupyter jupyterhub/jupyterhub --namespace kubeflow --create-namespace --values values.yaml
6. replace minio access key and secret key on notebook
7. run notebook


## Scratch

ijXzwIb2dknvV9YLNGJ0
gEL3Roc6BHaszN8LtWXjO35y4wlMOcRfLpPtitY4