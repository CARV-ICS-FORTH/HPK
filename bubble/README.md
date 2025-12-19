# Test for HPK bubbles

Package and push container with:
```bash
docker build -t chazapis/hpk-bubble:3 .
docker push chazapis/hpk-bubble:3
```

Now you need two VMs: 192.168.64.9 and 192.168.64.10.

The first one (192.168.64.9) also runs etcd:
```bash
HOST_ADDRESS=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')
echo $HOST_ADDRESS
cat >>/etc/default/etcd <<EOF
ETCD_LISTEN_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
ETCD_ADVERTISE_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
EOF
systemctl enable etcd
```

Run in first VM (192.168.64.9):
```bash
./bubble.sh 1

apptainer shell instance://bubble1
flanneld -etcd-endpoints http://192.168.64.9:2379 -ip-masq -iface tap0 -public-ip 192.168.64.9
```

Run in second VM (192.168.64.10):
```bash
./bubble.sh 2

apptainer shell instance://bubble2
flanneld -etcd-endpoints http://192.168.64.9:2379 -ip-masq -iface tap0 -public-ip 192.168.64.10
```

Now connect to the first bubble again and try to start a container within:
```bash
apptainer shell instance://bubble1

curl -Lo /usr/libexec/apptainer/cni/flannel https://github.com/flannel-io/cni-plugin/releases/download/v1.8.0-flannel2/flannel-arm64
chmod +x /usr/libexec/apptainer/cni/flannel

cat > /etc/apptainer/network/40_flannel.conflist <<EOF
{
    "cniVersion": "1.0.0",
    "name": "flannel",
    "plugins": [
        {
            "type": "flannel",
            "delegate": {
                "isDefaultGateway": true
            }
        },
        {
            "type": "firewall"
        },
        {
            "type": "portmap",
            "capabilities": {"portMappings": true},
            "snat": true
        }
    ]
}
EOF
```

Try it out:
```bash
apptainer instance run \
    --network=flannel \
    --containall \
    docker://ubuntu:24.04 \
    ubuntu

apptainer --debug exec \
    --network=flannel \
    docker://alpine true

apptainer exec \
    --containall \
    docker://alpine true
```
