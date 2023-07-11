#!/bin/bash

HOST_ADDRESS=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')

# Configure etcd, needed by Flannel to store configuration (install on one node only)
export ETCDCTL_API=3
export ETCD_ADVERTISE_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
export ETCD_LISTEN_CLIENT_URLS="http://${HOST_ADDRESS}:2379"

# Start etcd server
etcd & &>/var/log/etcd

# Put Flannel Configuration
#

