from dask.distributed import Client, performance_report
import dask.array as da
import time
import os
import re
import ast

SIZE = int(os.environ.get("MATRIX_SIZE", 8000))
CHUNK = int(os.environ.get("CHUNK_SIZE", 1000))
REPORT_PATH = "/report/dask-matrix-report.html"

def extract_metrics(path):
    with open(path, "r") as f:
        html_content = f.read()

    text = html_content.replace('\xa0', ' ')

    # Extract duration
    duration_match = re.search(r"Duration:\s*([\d.]+)\s*s", text, re.IGNORECASE)
    duration = float(duration_match.group(1)) if duration_match else None

    # Extract compute time
    compute_match = re.search(r"compute\s*time:\s*(\d+)m\s*(\d+)s", text, re.IGNORECASE)
    compute_time = int(compute_match.group(1)) * 60 + int(compute_match.group(2)) if compute_match else None

    # Extract cpu metrics
    cpu_match = re.search(r'\["cpu",\["min: [\d\.]+%","max: ([\d\.]+)%","mean: ([\d\.]+)%"\]\]', text)

    if cpu_match:
        max_cpu = float(cpu_match.group(1))
        mean_cpu = float(cpu_match.group(2))
    else:
        max_cpu = mean_cpu = None

    return duration, compute_time, max_cpu, mean_cpu

# Connect to Dask scheduler inside the cluster
client = Client("tcp://dask-scheduler.dask-matrix.svc.cluster.local:8786")

print("Connected to Dask cluster")

print(f"Creating arrays of shape ({SIZE}, {SIZE}) with chunks ({CHUNK}, {CHUNK})")

a = da.random.random((SIZE, SIZE), chunks=(CHUNK, CHUNK))
b = da.random.random((SIZE, SIZE), chunks=(CHUNK, CHUNK))

with performance_report(filename=REPORT_PATH):
    c = da.matmul(a, b).compute()

duration, compute_time, max_cpu, mean_cpu = extract_metrics(REPORT_PATH)
print("Duration:", duration)
print("Compute time:", compute_time)
print("Max CPU:", max_cpu)
print("Mean CPU:", mean_cpu)

