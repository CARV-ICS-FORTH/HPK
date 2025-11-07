# Dask Matrix Multiplication Benchmark

This project demonstrates running a large-scale matrix multiplication workload
on a [Dask](https://www.dask.org/) cluster inside Kubernetes. It connects to a Dask scheduler service,
generates large random matrices, performs multiplication, and produces
a Dask performance report in HTML format.

## Overview
The workflow consists of:
1. **Cluster Deployment**
    - `install.sh` uses Helm to deploy a Dask cluster into Kubernetes with resource settings from `values.yaml`
    - `uninstall.sh` tears it down when no longer needed 
2. **Python script** (`benchmark.py`)
    - Connects to a running Dask cluster
    - Creates two random matrices (SIZE × SIZE) chunked into smaller blocks
    - Multiplies the matrices using Dask's distributed computation 
    - Records execution time
    - Generates an HTML performance report
    - Extracts only the relevant metrics from the report (duration, compute time, CPU utilization)
3. **Docker image**
    - Packages the script and dependencies into a runnable container
4. **Kubernetes Job** (`dask-matrix-job.yaml`)
    - Launches the benchmark client inside the cluster
---------------------------------------------------------------------------------------------------
## Deploy the Dask Cluster
Deploy the cluster with Helm:
```
./install.sh
```
Optionally, change the cluster's resource configuration by editing `values.yaml`.


Tear down when done:
```
./uninstall.sh
```
---------------------------------------------------------------------------------------------------
## Docker image
Optionally, build and push to your registry:
```
docker build -t <your-username>/dask-matrix-client .
docker push <your-username>/dask-matrix-client
```
---------------------------------------------------------------------------------------------------
## Kubernetes Job
Run the benchmark as a Kubernetes job:
```
kubectl apply -f dask-matrix-job.yaml
```
Optionally, tune the workload size through the environment variables:
- `MATRIX_SIZE` - size of the square matrices
- `CHUNK_SIZE` - chunk size for Dask arrays
---------------------------------------------------------------------------------------------------
## Logs
Get the job's logs which include the duration, compute time, max and mean CPU utilization with:
```
kubectl logs job/dask-matrix-client
```
