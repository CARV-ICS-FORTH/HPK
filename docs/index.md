
<div class="ascii-art" style="font-family: monospace;white-space: pre; line-height: 1;">
██   ██ ██████  ██   ██ 
██   ██ ██   ██ ██  ██  
███████ ██████  █████   
██   ██ ██      ██  ██  
██   ██ ██      ██   ██ 
</div>

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

<div class="text-center">
<a href="admin-guide.html" class="btn btn-primary" role="button">Admin Guide</a>
<a href="examples.html" class="btn btn-primary" role="button">Examples</a>
</div>