# OpenMPI

Compile the `code` contents into binaries that will be stored in `~/scratch/openmpi/binaries`.

```shell
mkdir -p ~/scratch/openmpi/binaries
kubectl apply -f build
```

The binaries will be subsequently used in `patterns`.