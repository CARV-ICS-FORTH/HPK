FROM python:3.10.12-slim

RUN pip install \
    beautifulsoup4 \
    dask==2024.1.0 \
    distributed==2024.1.0 \
    numpy==1.26.3 \
    cloudpickle==3.0.0 \
    tornado==6.3.3 \
    toolz==0.12.0 \
    pandas==2.1.4 \
    lz4==4.3.3 \
    msgpack==1.0.7

WORKDIR /app
COPY benchmark.py /app/benchmark.py

ENTRYPOINT ["python", "benchmark.py"]
