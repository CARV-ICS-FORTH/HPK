#!/bin/bash

# Install HPK requirements.
# Tested on Ubuntu 20.04, CentOS 7.

APPTAINER_VERSION=1.1.4
FLANNEL_VERSION=0.20.2
FLANNEL_CNI_PLUGIN_VERSION=1.1.2
KUBERNETES_VERSION=1.24.8
HELM_VERSION=3.10.3

HOST_ADDRESS=$(ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')
if [[ -f /opt/slurm/etc/slurm_parallelcluster.conf ]]; then
    SLURM_CONFIG=/opt/slurm/etc/slurm_parallelcluster.conf
else
    if [[ ! -f /etc/slurm/slurm.conf ]]; then
        echo "ERROR: Slurm configuration file not found."
        exit 1
    fi
    SLURM_CONFIG=/etc/slurm/slurm.conf
fi
ETCD_ADDRESS=`cat ${SLURM_CONFIG} | grep SlurmctldHost | awk -F '[()]' '{print $2}'`

# Requirements
if [[ "$(. /etc/os-release; echo $ID)" == "ubuntu" ]]; then
    apt-get update
    apt-get install -y wget
else
    yum install -y wget
fi

if [[ "$HOST_ADDRESS" == "$ETCD_ADDRESS" ]]; then
    # Install etcd, needed by Flannel to store configuration
    if [[ "$(. /etc/os-release; echo $ID)" == "ubuntu" ]]; then
        apt-get install -y etcd-server etcd-client
        cat >>/etc/default/etcd <<EOF
ETCD_LISTEN_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
ETCD_ADVERTISE_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
EOF
    else
        yum install -y etcd
        sed -i "s/localhost:2379/${HOST_ADDRESS}:2379/" /etc/etcd/etcd.conf
    fi

    systemctl enable etcd
    systemctl restart etcd

    export ETCDCTL_API=3
    etcdctl --endpoints http://${HOST_ADDRESS}:2379 put /coreos.com/network/config '{"Network": "10.244.0.0/16", "Backend": {"Type": "vxlan"}}'
fi

# Install Apptainer
if [[ "$(. /etc/os-release; echo $ID)" == "ubuntu" ]]; then
    wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer_${APPTAINER_VERSION}_amd64.deb
    wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer-suid_${APPTAINER_VERSION}_amd64.deb
    apt-get install -y ./apptainer_${APPTAINER_VERSION}_amd64.deb ./apptainer-suid_${APPTAINER_VERSION}_amd64.deb
    rm -f apptainer_${APPTAINER_VERSION}_amd64.deb apptainer-suid_${APPTAINER_VERSION}_amd64.deb
else
    wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer-${APPTAINER_VERSION}-1.x86_64.rpm
    wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer-suid-${APPTAINER_VERSION}-1.x86_64.rpm
    yum install -y epel-release
    yum install -y fuse2fs
    yum install -y ./apptainer-${APPTAINER_VERSION}-1.x86_64.rpm ./apptainer-suid-${APPTAINER_VERSION}-1.x86_64.rpm
    rm -f apptainer-${APPTAINER_VERSION}-1.x86_64.rpm apptainer-suid-${APPTAINER_VERSION}-1.x86_64.rpm

    echo "user.max_user_namespaces=15000" > /etc/sysctl.d/90-max_user_namespaces.conf
    sysctl -p /etc/sysctl.d/90-max_user_namespaces.conf
fi

# Install Flannel to distribute private IPs across hosts
if [[ "$(. /etc/os-release; echo $ID)" == "ubuntu" ]]; then
    apt-get install -y nscd # https://github.com/flannel-io/flannel/issues/1512
fi

wget -q https://github.com/flannel-io/flannel/releases/download/v${FLANNEL_VERSION}/flanneld-amd64
chmod +x flanneld-amd64
cp flanneld-amd64 /usr/local/bin/flanneld
rm -f flanneld-amd64

cat >/etc/systemd/system/flanneld.service <<EOF
[Unit]
Description=flannel daemon

[Service]
ExecStart=/usr/local/bin/flanneld -etcd-endpoints http://${ETCD_ADDRESS}:2379 -ip-masq
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
cp flannel-amd64 /usr/libexec/apptainer/cni/flannel
rm -f flannel-amd64

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

# Install utilities
wget -q https://dl.k8s.io/v${KUBERNETES_VERSION}/bin/linux/amd64/kubectl
chmod +x kubectl
cp kubectl /usr/local/bin/kubectl
rm -f kubectl

wget -q https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz
tar -zxvf helm-v${HELM_VERSION}-linux-amd64.tar.gz --strip-components=1 linux-amd64/helm
cp helm /usr/local/bin/helm
rm -f helm helm-v${HELM_VERSION}-linux-amd64.tar.gz

exit 0
