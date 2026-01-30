#!/bin/bash

HPK_MASTER_CONF_DIR=/usr/local/etc
HPK_MASTER_LOG_DIR=/var/log/hpk-master

mkdir -p ${HPK_MASTER_LOG_DIR}
mkdir -p ${HPK_MASTER_CONF_DIR}/kubernetes

export IP_ADDRESS=`ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p'`

rm -f ${HPK_MASTER_CONF_DIR}/kubernetes/admin.conf

k3s server \
  --disable-agent \
  --disable scheduler \
  --disable coredns \
  --disable servicelb \
  --disable traefik \
  --disable local-storage \
  --disable metrics-server \
  --disable-cloud-controller \
  --write-kubeconfig-mode 777 \
  --node-ip=${IP_ADDRESS} \
  --bind-address=${IP_ADDRESS} \
  --https-listen-port=443 \
  --write-kubeconfig ${HPK_MASTER_CONF_DIR}/kubernetes/admin.conf \
  --egress-selector-mode disabled \
  --kube-apiserver-arg=enable-aggregator-routing=true \
  &> ${HPK_MASTER_LOG_DIR}/k3s.log &

echo -e "\n----------\nWaiting for K3s server to be created...\n----------"
while [ ! -f ${HPK_MASTER_CONF_DIR}/kubernetes/admin.conf ]; do
  sleep 1
done

export KUBECONFIG=${HPK_MASTER_CONF_DIR}/kubernetes/admin.conf

echo -e "Waiting for Kubernetes API server to be ready...\n----------"
until k3s kubectl get nodes &>/dev/null; do
  sleep 1
done

echo -e "K3s server started\n----------"

cat > /etc/resolv.conf <<EOF
nameserver 127.0.0.1
search svc.cluster.local cluster.local
options ndots:5
EOF

# Run Core DNS Here, after k3s server is up and running
mkdir -p ${HPK_MASTER_CONF_DIR}/coredns
cat > ${HPK_MASTER_CONF_DIR}/coredns/Corefile <<EOF
.:53 {
    errors
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        endpoint 127.0.0.1:443
        kubeconfig ${KUBECONFIG}
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
        ttl 5
    }
    forward . ${EXTERNAL_DNS}
    cache 30
    loop
    reload
    loadbalance
}
EOF

coredns -conf ${HPK_MASTER_CONF_DIR}/coredns/Corefile \
  &> ${HPK_MASTER_LOG_DIR}/coredns.log &
cat <<EOF | k3s kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: kube-dns
  namespace: kube-system
spec:
  clusterIP: None
  ports:
  - name: dns
    port: 53
    protocol: UDP
  - name: dns-tcp
    port: 53
    protocol: TCP
---
apiVersion: v1
kind: Endpoints
metadata:
  name: kube-dns
  namespace: kube-system
subsets:
- addresses:
  - ip: ${IP_ADDRESS}
    nodeName: k8s-control
  ports:
  - name: dns
    port: 53
    protocol: UDP
  - name: dns-tcp
    port: 53
    protocol: TCP
EOF

# Prepare the keys for the services webhook
mkdir -p ${HPK_MASTER_CONF_DIR}/kubernetes/pki
cp /var/lib/rancher/k3s/server/tls/server-ca.crt ${HPK_MASTER_CONF_DIR}/kubernetes/pki/ca.crt
cp /var/lib/rancher/k3s/server/tls/server-ca.key ${HPK_MASTER_CONF_DIR}/kubernetes/pki/ca.key
(cd ${HPK_MASTER_CONF_DIR}/kubernetes/pki && generate-keys.sh)

# Start the services webhook
CA_BUNDLE=$(cat ${HPK_MASTER_CONF_DIR}/kubernetes/pki/ca.crt | base64 | tr -d '\n')
cat <<EOF | k3s kubectl apply -f -
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: services-webhook
webhooks:
  - name: services-webhook.default.svc
    clientConfig:
      url: "https://127.0.0.1:8443/mutate"
      caBundle: ${CA_BUNDLE}
    rules:
      - operations: ["CREATE", "UPDATE"]
        apiGroups: ["*"]
        apiVersions: ["*"]
        resources: ["services"]
        scope: "*"
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
EOF

services-webhook \
  -tlsCertFile ${HPK_MASTER_CONF_DIR}/kubernetes/pki/services-webhook.crt \
  -tlsKeyFile ${HPK_MASTER_CONF_DIR}/kubernetes/pki/services-webhook.key \
   &> ${HPK_MASTER_LOG_DIR}/services-webhook.log &

# Start the random scheduler
random-scheduler \
  &> ${HPK_MASTER_LOG_DIR}/random-scheduler.log &

k3s kubectl delete svc kubernetes -n default --ignore-not-found=true >/dev/null 2>&1

echo -e "----------\nWaiting for kubernetes Service to be recreated as headless...\n----------"
while true; do
  cip="$(k3s kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)"
  if [ "${cip}" = "None" ]; then
    break
  fi
  sleep 1
done

echo -e "HPK-master is ready!"

# Done
sleep infinity
