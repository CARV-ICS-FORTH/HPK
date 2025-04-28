#!/bin/bash

# Copyright © 2022 Antony Chazapis
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

K8SFS_CONF_DIR=/usr/local/etc
K8SFS_DATA_DIR=/var/lib/etcd
K8SFS_LOG_DIR=/var/log/k8sfs

export IP_ADDRESS=`ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p'`

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
  --bind-address ${IP_ADDRESS} \
  --node-ip=${IP_ADDRESS} \
  --write-kubeconfig /output/kubeconfig.yaml &

sleep 45

export KUBECONFIG=/output/kubeconfig.yaml

# Run Core DNS Here, after k3s server is up and running
mkdir -p ${K8SFS_CONF_DIR}/coredns
cat > ${K8SFS_CONF_DIR}/coredns/Corefile <<EOF
.:53 {
    errors
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        endpoint 127.0.0.1:443
        kubeconfig ${KUBECONFIG}
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
        ttl 5
    }
    forward . /etc/resolv.conf
    cache 30
    loop
    reload
    loadbalance
}
EOF

coredns -conf ${K8SFS_CONF_DIR}/coredns/Corefile \
  &> ${K8SFS_LOG_DIR}/coredns.log &
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

  # Generate the data encryption config and key
ENCRYPTION_KEY=$(head -c 32 /dev/urandom | base64)
mkdir -p ${K8SFS_CONF_DIR}/kubernetes
cat > ${K8SFS_CONF_DIR}/kubernetes/encryption-config.yaml <<EOF
kind: EncryptionConfig
apiVersion: v1
resources:
  - resources:
      - secrets
    providers:
      - aescbc:
          keys:
            - name: key1
              secret: ${ENCRYPTION_KEY}
      - identity: {}
EOF

# Start the services webhook
if [ "$K8SFS_HEADLESS_SERVICES" == "1" ]; then
    CA_BUNDLE=$(cat ${K8SFS_CONF_DIR}/kubernetes/pki/ca.crt | base64 | tr -d '\n')
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
      -tlsCertFile ${K8SFS_CONF_DIR}/kubernetes/pki/apiserver.crt \
      -tlsKeyFile ${K8SFS_CONF_DIR}/kubernetes/pki/apiserver.key \
      &> ${K8SFS_LOG_DIR}/services-webhook.log &

fi

# Start the random scheduler
if [ "$K8SFS_RANDOM_SCHEDULER" == "1" ]; then
    random-scheduler \
      &> ${K8SFS_LOG_DIR}/random-scheduler.log &
fi

CA_BUNDLE=$(cat ${K8SFS_CONF_DIR}/kubernetes/pki/ca.crt | base64 | tr -d '\n')

# Done
sleep infinity
