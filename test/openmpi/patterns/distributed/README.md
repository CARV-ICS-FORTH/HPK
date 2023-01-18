== Distributed MPI Tasks

This examples makes use of the kubeflow/mpi-operator in order to run launch distributed MPI tasks on Kubernetes.

Firstly, we need to run install the mpi-operator

```shell
kubectl apply -f https://raw.githubusercontent.com/kubeflow/mpi-operator/master/deploy/v2beta1/mpi-operator.yaml
```

and then we can run the MPI experiments.

```
kubectl apply -f mpi-run.yaml
```

The TensorFlow benchmark will launch a multi-node TensorFlow benchmark training job.

Similarly, we can launch multiple HPC jobs.

https://github.com/kubeflow/mpi-operator

For more examples, https://github.com/kubeflow/mpi-operator/tree/master/examples/v2beta1/pi