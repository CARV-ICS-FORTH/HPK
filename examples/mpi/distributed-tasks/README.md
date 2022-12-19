== Distributed MPI Tasks

This examples makes use of the kubeflow/mpi-operator in order to run launch distributed MPI tasks on Kubernetes.


Firstly, we need to run install the operator
```shell
kubectl apply -f mpi-operator.yaml
```

and then we can run the MPI experiments.

```
kubectl apply -f tensorbench.yaml
```

The TensorFlow benchmark will launch a multi-node TensorFlow benchmark training job.

Similarly, we can launch multiple HPC jobs.





https://github.com/kubeflow/mpi-operator