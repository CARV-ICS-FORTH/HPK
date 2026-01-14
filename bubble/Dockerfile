FROM ubuntu:24.04

ARG TARGETARCH

# Basic utilities
RUN apt-get update && \
    apt-get install -y vim-tiny curl iproute2 net-tools bridge-utils socat iputils-ping

# Apptainer
RUN apt-get install -y software-properties-common && \
    add-apt-repository -y ppa:apptainer/ppa && \
    apt-get install -y apptainer

# Etcd and Flannel
RUN apt-get install -y etcd-server etcd-client && \
    apt-get install -y iptables nscd && \
    curl -L -o /usr/local/bin/flanneld https://github.com/flannel-io/flannel/releases/download/v0.27.4/flanneld-${TARGETARCH} && \
    chmod +x /usr/local/bin/flanneld

# TAP traffic forwarder
COPY tap_forward.py /usr/local/bin/tap_forward
