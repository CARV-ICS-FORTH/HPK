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
make run-hpk-master
```

Once the master is up and running, you can start the `hpk-kubelet`:

```bash
make run-kubelet
```

Now you can configure and use `kubectl`:

```bash
export KUBE_PATH=~/.hpk-master/kubernetes/
export KUBECONFIG=${KUBE_PATH}/admin.conf
kubectl get nodes
```

In case that you experience DNS issues, you should retry starting the Kubernetes Master with:
```
export EXTERNAL_DNS=<your dns server>
make run-kubemaster
```

The above command will set CoreDNS to forward requests for external names to your DNS server.

## Publications/presentations

The latest paper on HPK [is available at arXiv](https://arxiv.org/abs/2409.16919). A [previous edition of this work](https://doi.org/10.1007/978-3-031-40843-4_14) was presented at WOCC'23 ("The International Workshop on Converged Computing on Edge, Cloud, and HPC", held in conjunction with ISC-HPC 2023).

The corresponding BibTeX entry is the following:
```bibtex
@misc{hpk,
      title={Running Cloud-native Workloads on HPC with High-Performance Kubernetes}, 
      author={Antony Chazapis and Evangelos Maliaroudakis and Fotis Nikolaidis and Manolis Marazakis and Angelos Bilas},
      year={2024},
      eprint={2409.16919},
      archivePrefix={arXiv},
      primaryClass={cs.DC},
      url={https://arxiv.org/abs/2409.16919}, 
}
```

HPK was presented at FOSDEM 2025; slides and video from the event are [available](https://fosdem.org/2025/schedule/event/fosdem-2025-5722-running-kubernetes-workloads-on-hpc-with-hpk/).

## Acknowledgements

We thankfully acknowledge the support of the European Commission and the Greek General Secretariat for Research and
Innovation to this project. HPK has received funding from the European Union’s Horizon Europe research and innovation programme through project RISER ("RISC-V for Cloud Services", GA-101092993), from the EuroHPC Joint Undertaking through projects EUPEX (GA-101033975) and DEEP-SEA (GA-955606), as well as from the Chips Joint Undertaking through project REBECCA ("Reconfigurable Heterogeneous Highly Parallel Processing Platform for safe and secure AI", GA-101097224). EuroHPC JU and Chips JU projects are jointly funded by the European Commission and the involved state members (including the Greek General Secretariat for Research and Innovation).
