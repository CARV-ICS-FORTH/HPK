# Spark Operator 

## Overview
- **Setup**
  `install.sh` installs prerequisites (Spark Operator, MinIO, etc.) and prepares the environment.
  `uninstall.sh` removes these components and the created Spark applications.
- **Data generation** (`manifest-tpcds-data-generation.yaml`)
  Uses Spark to generate TPC-DS data in Parquet format at the desired scale factor. The output is stored in MinIO/S3.
- **Benchmark execution** (`manifest-tpcds-benchmark.yaml`)
  Runs a set of TPC-DS SQL queries multiple times, storing benchmark results in MinIO/S3 for analysis.
- **Automation** (`tpcds.sh`)
  Automates the TPC-DS benchmark workflow by installing components, generating data, and running the benchmark.

------------------------------------------------------------------------------------------------

### TPC-DS Benchmark
This project demonstrates running the [TPC-DS](https://www.tpc.org/tpcds/) benchmark using
Apache Spark on Kubernetes with the [Spark Operator](https://github.com/kubeflow/spark-operator).
It consists of two stages:
1. **Data Generation** – Generates TPC-DS datasets at a configurable scale factor and stores them in MinIO/S3.
2. **Benchmark Execution** – Runs selected TPC-DS SQL queries against the generated data and writes results to S3.
------------------------------------------------------------------------------------------------
#### Usage
1. **Run the full TPC-DS workflow**


   Execute the automation script which handles environment setup, data generation, and benchmark execution:
   ```
   ./tpcds.sh
   ```
   The script will display status messages for each stage (Spark Operator & MinIO setup, data generation, benchmark).

3. **Check benchmark logs**


   Once the benchmark has finished, inspect the SQL query running times and results using:
   ```
   kubectl logs tpcds-benchmark-sql-1g-driver
   ```
   Replace the pod name if using a different namespace or scale factor.

5. **Cleanup**


   When finished, remove Spark Operator, MinIO, and the Spark application using:
   ```
   ./uninstall.sh
   ```
------------------------------------------------------------------------------------------------
#### Optional configuration
Several aspects of the TPC-DS benchmark workflow are customizable:
1. **Spark driver and executor resources**


   In the driver section, adjust CPU and memory. In the executor section, adjust the number of executors as well as their CPU and memory.
4. **TPC-DS scale factor**


   Controls the size of the generated dataset (in GB). It is set as the 4th argument in `manifest-tpcds-data-generation.yaml`.
6. **Queries to run**

   
   In `manifest-tpcds-benchmark.yaml`, you can specify which TPC-DS queries to run by editing the query list in arguments:
   ```
   - "q1-v2.4,q10-v2.4,q11-v2.4" # example of subset of queries
   ```
   - Leave empty to run all queries.
   - - You can also change the number of repetitions by modifying the corresponding argument.
