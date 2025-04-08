# Kubernetes from scratch

The purpose of this repository is to boostrap a very basic Kubernetes environment for experimenting with custom Kubernetes components, especially [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) implementations. Pre-built container images for various architectures are [available](https://hub.docker.com/r/chazapis/kubernetes-from-scratch).

Example usage:
```bash
docker run -d --rm -p 443:6443 --name k8sfs chazapis/kubernetes-from-scratch:<tag>
docker cp k8sfs:/root/.kube/config kubeconfig
sed -i 's/server:.*/server: https:\/\/127.0.0.1:6443/' kubeconfig
export KUBECONFIG=$PWD/kubeconfig
kubectl version
```

| Variable                  | Description                                      | Default |
|---------------------------|--------------------------------------------------|---------|
| `K8SFS_HEADLESS_SERVICES` | Start the webhook to make all services headless. | `1`     |
| `K8SFS_RANDOM_SCHEDULER`  | Start the random (pass-through) scheduler.       | `1`     |
| `K8SFS_MOCK_KUBELET`      | Start the mock kubelet.                          | `1`     |

Inspired by the excellent [Kubernetes The Hard Way](https://github.com/kelseyhightower/kubernetes-the-hard-way) and [Kubernetes Deployment From Scratch - The Ultimate Guide](https://www.ulam.io/blog/kubernetes-scratch).
