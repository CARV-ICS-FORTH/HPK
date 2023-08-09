# High-Performance Kubernetes

High-Performance [Kubernetes](https://kubernetes.io/) (HPK), allows HPC users to run their own private "mini Clouds" on
a typical HPC cluster. HPK uses [a single container](https://github.com/chazapis/kubernetes-from-scratch) to run the
Kubernetes control plane and a [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) Provider
implementation to translate container lifecycle management commands from Kubernetes-native
to [Slurm](https://slurm.schedmd.com/)/[Apptainer](https://github.com/apptainer/apptainer).

To allow users to run HPK, the HPC environment should have Apptainer configured so that:

* It allows users to run containers with `--fakeroot`.
* It uses a CNI plug-in that hands over private IPs to containers, which are routable across cluster hosts (we
  use [flannel](https://github.com/flannel-io/flannel) and
  the [flannel CNI plug-in](https://github.com/flannel-io/cni-plugin)).

In contrast to a typical Kubernetes installation at the Cloud:

* HPK uses a pass-through scheduler, which assigns all pods to the single `hpk-kubelet` that represents the cluster. In
  practice, this means that all scheduling is delegated to Slurm.
* All Kubernetes services are converted
  to [headless](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services). This avoids the
  need for internal, virtual cluster IPs that would need special handling at the network level. As a side effect, HPK
  services that map to multiple pods are load-balanced at the DNS level if clients support it.

HPK is a continuation of the [KNoC](https://github.com/CARV-ICS-FORTH/knoc) project, a Virtual Kubelet Provider implementation that can be used to bridge Kubernetes and HPC environments.

## Trying it out

First you need to configure Apptainer for HPK. The [install-environment.sh](test/install-environment.sh) script showcases how we implement the requirements in a single node for testing.

Once setup, compile the `hpk-kubelet` using `make`.

```bash
make build
```

Then you need to start the Kubernetes Master and `hpk-kubelet` seperately.

To run the Kubernetes Master:

```bash
make run-kubemaster
```

Once the master is up and running, you can start the `hpk-kubelet`:

```bash
make run-kubelet
```

Now you can configure and use `kubectl`:

```bash
export KUBE_PATH=~/.k8sfs/kubernetes/
export KUBECONFIG=${KUBE_PATH}/admin.conf
kubectl get nodes
```

In case that you experience DNS issues, you should retry starting the Kubernetes Master with:
```
export EXTERNAL_DNS=<your dns server>
make run-kubemaster
```

The above command will set CoreDNS to forward requests for external names to your DNS server.


## Acknowledgements

We thankfully acknowledge the support of the European Commission and the Greek General Secretariat for Research and
Innovation under the EuroHPC Programme through projects EUPEX (GA-101033975) and DEEP-SEA (GA-955606). National
contributions from the involved state members (including the Greek General Secretariat for Research and Innovation)
match the EuroHPC funding.
