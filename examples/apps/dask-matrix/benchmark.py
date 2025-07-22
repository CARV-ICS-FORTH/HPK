from dask.distributed import Client, performance_report
import dask.array as da
import time
import os

SIZE = int(os.environ.get("MATRIX_SIZE", 8000))
CHUNK = int(os.environ.get("CHUNK_SIZE", 1000))

# Connect to Dask scheduler inside the cluster
client = Client("tcp://dask-scheduler.dask-matrix.svc.cluster.local:8786")

print("Connected to Dask cluster")

print(f"Creating arrays of shape ({SIZE}, {SIZE}) with chunks ({CHUNK}, {CHUNK})")

a = da.random.random((SIZE, SIZE), chunks=(CHUNK, CHUNK))
b = da.random.random((SIZE, SIZE), chunks=(CHUNK, CHUNK))

with performance_report(filename="/report/dask-matrix-report.html"):
    start = time.time()
    c = da.matmul(a, b).compute()
    end = time.time()
    print(f"Matrix multiplication completed in {end - start:.2f} seconds")
