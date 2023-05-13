#!/bin/bash

APPTAINER_VERSION=1.1.4
FLANNEL_VERSION=0.20.2
FLANNEL_CNI_PLUGIN_VERSION=1.1.2
KUBERNETES_VERSION=1.24.8
HELM_VERSION=3.10.3

HOST_ADDRESS=$(hostname -i)
ETCD_ADDRESS=$(cat /opt/slurm/etc/slurm_parallelcluster.conf | grep SlurmctldHost | awk -F '[()]' '{print $2}')

apt-get update

if [[ "$HOST_ADDRESS" == "$ETCD_ADDRESS" ]]; then
    # Install etcd, needed by Flannel to store configuration
    apt-get install -y etcd-server etcd-client

    cat >>/etc/default/etcd <<EOF
ETCD_LISTEN_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
ETCD_ADVERTISE_CLIENT_URLS="http://${HOST_ADDRESS}:2379"
EOF
    systemctl restart etcd

    export ETCDCTL_API=3
    etcdctl --endpoints http://${HOST_ADDRESS}:2379 put /coreos.com/network/config '{"Network": "10.244.0.0/16", "Backend": {"Type": "vxlan"}}'
fi

# Install Apptainer
wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer_${APPTAINER_VERSION}_amd64.deb
wget -q https://github.com/apptainer/apptainer/releases/download/v${APPTAINER_VERSION}/apptainer-suid_${APPTAINER_VERSION}_amd64.deb
apt-get install -y ./apptainer_${APPTAINER_VERSION}_amd64.deb ./apptainer-suid_${APPTAINER_VERSION}_amd64.deb
rm -f apptainer_${APPTAINER_VERSION}_amd64.deb apptainer-suid_${APPTAINER_VERSION}_amd64.deb

# Install Flannel to distribute private IPs across hosts
apt-get install -y nscd # https://github.com/flannel-io/flannel/issues/1512

wget -q https://github.com/flannel-io/flannel/releases/download/v${FLANNEL_VERSION}/flanneld-amd64
chmod +x flanneld-amd64
mv flanneld-amd64 /usr/local/bin/flanneld

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

# Install utilities
wget -q https://dl.k8s.io/v${KUBERNETES_VERSION}/bin/linux/amd64/kubectl
chmod +x kubectl
mv kubectl /usr/local/bin/kubectl

wget -q https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz
tar -zxvf helm-v${HELM_VERSION}-linux-amd64.tar.gz --strip-components=1 linux-amd64/helm
mv helm /usr/local/bin/helm
rm -f helm-v${HELM_VERSION}-linux-amd64.tar.gz

exit 0
