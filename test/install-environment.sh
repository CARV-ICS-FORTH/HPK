#!/bin/bash

APPTAINER_VERSION=1.1.4
FLANNEL_VERSION=0.20.2
FLANNEL_CNI_PLUGIN_VERSION=1.1.2
KUBERNETES_VERSION=1.24.8
HELM_VERSION=3.10.3

HOST_NAME=$(hostname -s)
HOST_ADDRESS=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')

CPUS=$(lscpu | grep -E '^CPU\(' | cut -d':' -f2 | tr -d '[:space:]')
SOCKETS=$(lscpu | grep -E '^Socket' | cut -d':' -f2 | tr -d '[:space:]')
CORES_PER_SOCKET=$(lscpu | grep -E '^Core' | cut -d':' -f2 | tr -d '[:space:]')
THREADS_PER_CORE=$(lscpu | grep -E '^Thread' | cut -d':' -f2 | tr -d '[:space:]')

# Requirements
apt-get update
apt-get install -y wget

# Install Apptainer
wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer_${APPTAINER_VERSION}_amd64.deb
wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer-suid_${APPTAINER_VERSION}_amd64.deb
apt-get install -y ./apptainer_${APPTAINER_VERSION}_amd64.deb ./apptainer-suid_${APPTAINER_VERSION}_amd64.deb
rm -f apptainer_${APPTAINER_VERSION}_amd64.deb apptainer-suid_${APPTAINER_VERSION}_amd64.deb

# Install etcd, needed by Flannel to store configuration
apt-get install -y etcd-server etcd-client

cat >>/etc/default/etcd <<EOF
ETCD_LISTEN_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
ETCD_ADVERTISE_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
EOF
systemctl restart etcd

export ETCDCTL_API=3
etcdctl --endpoints http://${HOST_ADDRESS}:2379 put /coreos.com/network/config '{"Network": "10.244.0.0/16", "Backend": {"Type": "vxlan"}}'

# Install Flannel to distribute private IPs across hosts
apt-get install -y nscd # https://github.com/flannel-io/flannel/issues/1512

wget -q https://github.com/flannel-io/flannel/releases/download/v${FLANNEL_VERSION}/flanneld-amd64
chmod +x flanneld-amd64
mv flanneld-amd64 /usr/local/bin/flanneld

cat >/etc/systemd/system/flanneld.service <<EOF
[Unit]
Description=flannel daemon

[Service]
ExecStart=/usr/local/bin/flanneld -etcd-endpoints http://${HOST_ADDRESS}:2379 -ip-masq
Restart=always

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable flanneld
systemctl start flanneld

# Install the Flannel CNI plug-in in Apptainer and configure Apptainer to use Flannel for fakeroot runs
wget -q https://github.com/flannel-io/cni-plugin/releases/download/v${FLANNEL_CNI_PLUGIN_VERSION}/flannel-amd64
chmod +x flannel-amd64
mv flannel-amd64 /usr/libexec/apptainer/cni/flannel

cat > /etc/apptainer/network/40_fakeroot.conflist <<EOF
{
    "cniVersion": "1.0.0",
    "name": "fakeroot",
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

# Install Slurm
apt-get install -y slurmd slurm-client slurmctld

cat >/etc/slurm/slurm.conf << EOF
AuthType=auth/none
CredType=cred/none
MpiDefault=none
ProctrackType=proctrack/linuxproc
ReturnToService=1
SlurmctldPidFile=/var/run/slurmctld.pid
SlurmctldPort=6817
SlurmdPidFile=/var/run/slurmd.pid
SlurmdPort=6818
SlurmdSpoolDir=/var/spool/slurmd
SlurmUser=root
StateSaveLocation=/var/spool
SwitchType=switch/none
TaskPlugin=task/affinity
InactiveLimit=0
KillWait=30
MinJobAge=300
SlurmctldTimeout=120
SlurmdTimeout=300
Waittime=0
SchedulerType=sched/backfill
SelectType=select/cons_tres
SelectTypeParameters=CR_Core
AccountingStorageType=accounting_storage/none
ClusterName=cluster
JobCompType=jobcomp/none
JobAcctGatherFrequency=30
JobAcctGatherType=jobacct_gather/none
SlurmctldDebug=info
SlurmdDebug=info

SlurmctldHost=${HOST_NAME}
NodeName=${HOST_NAME} CPUs=${CPUS} SocketsPerBoard=${SOCKETS} CoresPerSocket=${CORES_PER_SOCKET} ThreadsPerCore=${THREADS_PER_CORE} State=UNKNOWN
PartitionName=hpk Nodes=${HOST_NAME} Default=YES MaxTime=INFINITE State=UP
EOF
systemctl start slurmd
systemctl start slurmctld

# Install utilities
wget -q https://dl.k8s.io/v${KUBERNETES_VERSION}/bin/linux/amd64/kubectl
chmod +x kubectl
mv kubectl /usr/local/bin/kubectl

wget -q https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz
tar -zxvf helm-v${HELM_VERSION}-linux-amd64.tar.gz --strip-components=1 linux-amd64/helm
mv helm /usr/local/bin/helm
rm -f helm-v${HELM_VERSION}-linux-amd64.tar.gz
