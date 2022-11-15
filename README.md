# High-Performance Kubernetes

High-Performance [Kubernetes](https://kubernetes.io/) (HPK), allows HPC users to run their own private "mini Clouds" on a typical HPC cluster. HPK uses [a single container](https://github.com/chazapis/kubernetes-from-scratch) to run the Kubernetes control plane and a [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) Provider implementation to translate container lifecycle management commands from Kubernetes-native to [Slurm](https://slurm.schedmd.com/)/[Apptainer](https://github.com/apptainer/apptainer).

To allow users to run HPK, the HPC environment should have Apptainer configured so that:
* It allows users to run containers with `--fakeroot`.
* It uses a CNI plug-in that hands over private IPs to containers, which are routable across cluster hosts (we use [flannel](https://github.com/flannel-io/flannel) and the [flannel CNI plug-in](https://github.com/flannel-io/cni-plugin)).

In contrast to a "typical" Kubernetes installation at the Cloud:
* HPK uses a pass-through scheduler, which assigns all pods to the single `hpk-kubelet` that represents the cluster. In practice, this means that all scheduling is delegated to Slurm.
* All Kubernetes services are converted to [headless](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services). This avoids the need for internal, virtual cluster IPs that would need special handling at the network level. As a side effect, HPK services that map to multiple pods are load-balanced at the DNS level if clients support it.

## Running

Compile using `make`. To run:

```bash
# Start the control plane
mkdir -p .k8sfs
apptainer run --net --dns 8.8.8.8 --fakeroot \
  --cleanenv --pid --containall \
  --no-init --no-umask --no-eval \
  --no-mount tmp,home --unsquash --writable \
  --env K8SFS_MOCK_KUBELET=0 \
  --bind .k8sfs:/usr/local/etc \
  docker://chazapis/kubernetes-from-scratch:20221115

# Run hpk-kubelet
export KUBECONFIG=$HOME/.k8sfs/kubernetes/admin.conf
export APISERVER_KEY_LOCATION=$HOME/.k8sfs/kubernetes/pki/admin.key
export APISERVER_CERT_LOCATION=$HOME/.k8sfs/kubernetes/pki/admin.crt
./bin/hpk-kubelet
```

## Acknowledgements

We thankfully acknowledge the support of the European Commission and the Greek General Secretariat for Research and Innovation under the EuroHPC Programme through projects EUPEX (GA-101033975) and DEEP-SEA (GA-955606). National contributions from the involved state members (including the Greek General Secretariat for Research and Innovation) match the EuroHPC funding.
