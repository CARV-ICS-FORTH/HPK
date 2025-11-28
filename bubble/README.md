# Test for HPK bubbles

Package and push container with:
```bash
docker build -t chazapis/hpk-bubble:2 .
docker push chazapis/hpk-bubble:2
```

Run with:
```bash
./bubble.sh 1
```

Connect:
```bash
apptainer shell instance://bubble1
```

Inside the container:
```bash
flanneld -etcd-endpoints http://<etcd host>:2379 -ip-masq
```
