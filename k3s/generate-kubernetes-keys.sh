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

if [ -z "$IP_ADDRESS" ]; then echo "Empty IP_ADDRESS"; exit 1; fi

# Certificate authority
openssl genrsa -out ca.key 2048
openssl req -x509 -new -nodes -key ca.key -days 365 -out ca.crt -subj "/CN=kubernetes" \
  -addext "basicConstraints=CA:TRUE" \
  -addext "keyUsage=digitalSignature,keyEncipherment,keyCertSign"

# Key and certificate for etcd
openssl genrsa -out etcd.key 2048
openssl req -x509 -key etcd.key -CA ca.crt -CAkey ca.key -days 365 -nodes -out etcd.crt -subj "/CN=etcd" \
  -addext "basicConstraints=CA:FALSE" \
  -addext "keyUsage=digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth,clientAuth" \
  -addext "subjectAltName=IP:127.0.0.1,IP:${IP_ADDRESS}"

# Key and certificate for the API server
KUBERNETES_HOSTNAMES=DNS:kubernetes,DNS:kubernetes.default,DNS:kubernetes.default.svc,DNS:kubernetes.default.svc.cluster,DNS:kubernetes.svc.cluster.local
openssl genrsa -out apiserver.key 2048
openssl req -x509 -key apiserver.key -CA ca.crt -CAkey ca.key -days 365 -nodes -out apiserver.crt -subj "/CN=kube-apiserver" \
  -addext "basicConstraints=CA:FALSE" \
  -addext "keyUsage=digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth,clientAuth" \
  -addext "subjectAltName=IP:127.0.0.1,IP:${IP_ADDRESS},${KUBERNETES_HOSTNAMES}"

# Keys for service account tokens
openssl genrsa -out sa.key 2048
openssl rsa -in sa.key -pubout -out sa.pub

# Configuration for controller manager
openssl genrsa -out controller-manager.key 2048
openssl req -x509 -key controller-manager.key -CA ca.crt -CAkey ca.key -days 365 -nodes -out controller-manager.crt -subj "/CN=system:kube-controller-manager" \
  -addext "basicConstraints=CA:FALSE" \
  -addext "keyUsage=digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=clientAuth"

k3s kubectl config set-cluster kubernetes \
  --certificate-authority=ca.crt \
  --embed-certs=true \
  --server=https://127.0.0.1:443 \
  --kubeconfig=controller-manager.conf
k3s kubectl config set-context system:kube-controller-manager@kubernetes \
  --cluster=kubernetes \
  --user=system:kube-controller-manager \
  --kubeconfig=controller-manager.conf
k3s kubectl config set-credentials system:kube-controller-manager \
  --client-certificate=controller-manager.crt \
  --client-key=controller-manager.key \
  --embed-certs=true \
  --kubeconfig=controller-manager.conf
k3s kubectl config use-context system:kube-controller-manager@kubernetes \
  --kubeconfig=controller-manager.conf

# Configuration for admin
openssl genrsa -out admin.key 2048
openssl req -x509 -key admin.key -CA ca.crt -CAkey ca.key -days 365 -nodes -out admin.crt -subj "/CN=kubernetes-admin/O=system:masters" \
  -addext "basicConstraints=CA:FALSE" \
  -addext "keyUsage=digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=clientAuth"

k3s kubectl config set-cluster kubernetes \
  --certificate-authority=ca.crt \
  --embed-certs=true \
  --server=https://${IP_ADDRESS}:443 \
  --kubeconfig=admin.conf
k3s kubectl config set-context kubernetes-admin@kubernetes \
  --cluster=kubernetes \
  --user=kubernetes-admin \
  --kubeconfig=admin.conf
k3s kubectl config set-credentials kubernetes-admin \
  --client-certificate=admin.crt \
  --client-key=admin.key \
  --embed-certs=true \
  --kubeconfig=admin.conf
k3s kubectl config use-context kubernetes-admin@kubernetes \
  --kubeconfig=admin.conf
