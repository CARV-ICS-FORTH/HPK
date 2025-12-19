# RUN INTERNAL CONTAINERS

apptainer exec --network=none --writable-tmpfs docker://chazapis/hpk-bubble:3 /bin/bash

# INSIDE BUBBLE

ip tuntap add dev tap2 mode tap
ip link set tap2 up

socat -d -d \
  TUN,tun-name=tap2,tun-type=tap,iff-no-pi \
  UNIX-LISTEN:/tmp/tap-2.sock

ip tuntap add dev tap3 mode tap
ip link set tap3 up

socat -d -d \
  TUN,tun-name=tap3,tun-type=tap,iff-no-pi \
  UNIX-LISTEN:/tmp/tap-3.sock

ip link add name br0 type bridge
ip addr add 10.0.0.1/24 dev br0
ip link set tap2 master br0
ip link set tap3 master br0
ip link set br0 up

sysctl -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -o tap0 -j MASQUERADE
iptables -A FORWARD -i br0 -o tap0 -m state --state RELATED,ESTABLISHED -j ACCEPT
iptables -A FORWARD -i tap0 -o br0 -j ACCEPT

# INSIDE CONTAINER 1

ip tuntap add tap0 mode tap
ip link set tap0 up
ip addr add 10.0.0.2/24 dev tap0

socat -d -d \
  TUN,tun-name=tap0,tun-type=tap,iff-no-pi \
  UNIX-CONNECT:/tmp/tap-2.sock &

ip route add default via 10.0.0.1 dev tap0

# INSIDE CONTAINER 2

ip tuntap add tap0 mode tap
ip link set tap0 up
ip addr add 10.0.0.3/24 dev tap0

socat -d -d \
  TUN,tun-name=tap0,tun-type=tap,iff-no-pi \
  UNIX-CONNECT:/tmp/tap-3.sock &

ip route add default via 10.0.0.1 dev tap0
