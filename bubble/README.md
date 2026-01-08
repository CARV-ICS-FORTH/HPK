# HPK bubbles

A bubble is a container running the Kubernetes environment in a node (the kubelet). It hosts internal containers (pods) that run applications and services.
Bubbles use slirp4netns to assume a unique private IP that is only visible/routable from within the bubble. They can, however, run services and expose them at the host level via port binding.
Bubbles communicate across hosts via Flannel, a utility that implements an overlay network via VXLAN tunneling.

This folder contains instructions to build the bubble container image and test it.

## Building

Package and push the bubble container with:
```bash
docker build -t chazapis/hpk-bubble:4 .
docker push chazapis/hpk-bubble:4
```

## Test internal container networking within a bubble

In the host, create the bubble:
```bash
./bubble.sh 1
```

In another shell, connect to the bubble:
```bash
apptainer shell instance://bubble1
```

And create the device for the container:
```bash
ip tuntap add dev tap2 mode tap
ip addr add 10.0.0.1/24 dev tap2
ip link set tap2 up

sysctl -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 10.0.0.0/24 -o tap0 -j MASQUERADE

tap_forward --tap tap2 --socket /tmp/tap-2.sock --mode serve
```

Reconnect to the bubble and start an internal container:
```bash
apptainer exec --network=none --writable-tmpfs docker://chazapis/hpk-bubble:4 /bin/bash
```

Inside the container:
```bash
ip tuntap add tap0 mode tap
ip link set tap0 up
ip addr add 10.0.0.2/24 dev tap0

ip route add default via 10.0.0.1 dev tap0

tap_forward --tap tap0 --socket /tmp/tap-2.sock --mode connect &
```

The internal container should now have a working connection to the outside world with a unique private IP.

# Test intra-container networking (within a bubble)

In the host, create the bubble:
```bash
./bubble.sh 1
```

In another shell, connect to the bubble:
```bash
apptainer shell instance://bubble1
```

And create the devices for the containers:
```bash
# Device for the first container
ip tuntap add dev tap2 mode tap
ip link set tap2 up

tap_forward --tap tap2 --socket /tmp/tap-2.sock --mode serve &

# Device for the second container
ip tuntap add dev tap3 mode tap
ip link set tap3 up

tap_forward --tap tap3 --socket /tmp/tap-3.sock --mode serve &

# Bridge the devices, set IP of local endpoint
ip link add name br0 type bridge
ip addr add 10.0.0.1/24 dev br0
ip link set tap2 master br0
ip link set tap3 master br0
ip link set br0 up

# Set up NAT
sysctl -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 10.0.0.0/24 -o tap0 -j MASQUERADE
```

Reconnect to the bubble and start the first internal container:
```bash
apptainer exec --network=none --writable-tmpfs docker://chazapis/hpk-bubble:4 /bin/bash
```

Inside the container:
```bash
ip tuntap add tap0 mode tap
ip link set tap0 up
ip addr add 10.0.0.2/24 dev tap0

ip route add default via 10.0.0.1 dev tap0

tap_forward --tap tap0 --socket /tmp/tap-2.sock --mode connect &
```

Reconnect to the bubble and start the second internal container:
```bash
apptainer exec --network=none --writable-tmpfs docker://chazapis/hpk-bubble:4 /bin/bash
```

Inside the container:
```bash
ip tuntap add tap0 mode tap
ip link set tap0 up
ip addr add 10.0.0.3/24 dev tap0

ip route add default via 10.0.0.1 dev tap0

tap_forward --tap tap0 --socket /tmp/tap-3.sock --mode connect &
```

The two internal containers should be able to communicate with each other.

# Test inter-container networking (across bubbles) **[WIP]**

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
