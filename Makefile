# go options

GO111MODULE := on
export GO111MODULE

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell git))
BUILD_VERSION=$(git describe --tags --always --dirty="-dev")
else
BUILD_VERSION='unknown'
endif

BUILD_DATE ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
VERSION_FLAGS := -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'

VERSION = $(shell cat VERSION)

# Deployment options
HPK_MASTER_PATH ?= ${HOME}/.hpk-master
KUBE_PATH ?= ${HPK_MASTER_PATH}/kubernetes
EXTERNAL_DNS ?= 8.8.8.8

REGISTRY_NAME ?= carvicsforth

K3S_IMAGE_TAG=$(REGISTRY_NAME)/hpk-master:$(VERSION)

export PAUSE_IMAGE_TAG=$(REGISTRY_NAME)/pause:$(VERSION)

define WEBHOOK_CONFIGURATION
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: mutating-webhook
webhooks:
  - name: "pod-mutator.hpk.dev"
    rules:
      - apiGroups:   [""]
        apiVersions: ["v1"]
        operations:  ["CREATE"]
        resources:   ["pods"]
        scope:       "Namespaced"
    clientConfig:
      url: "https://${HOST_ADDRESS}:10250/mutates/pod"
      caBundle: ${CA_BUNDLE}
    failurePolicy: Fail
    admissionReviewVersions: ["v1"]
    timeoutSeconds: 5
    sideEffects: None
  - name: "pvc-mutator.hpk.dev"
    rules:
      - apiGroups:   [""]
        apiVersions: ["v1"]
        operations:  ["CREATE"]
        resources:   ["persistentvolumeclaims"]
        scope:       "Namespaced"
    clientConfig:
      url: "https://${HOST_ADDRESS}:10250/mutates/pvc"
      caBundle: ${CA_BUNDLE}
    failurePolicy: Fail
    admissionReviewVersions: ["v1"]
    timeoutSeconds: 5
    sideEffects: None
endef
export WEBHOOK_CONFIGURATION

define CERTIFICATE_CONFIGURATION
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name

[req_distinguished_name]

[v3_req]
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth, clientAuth
subjectAltName = @alt_names

[alt_names]
IP.1 = 127.0.0.1
IP.2 = ${HOST_ADDRESS}
endef
export CERTIFICATE_CONFIGURATION

##@ General

.DEFAULT_GOAL := help

help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build

build: hpk-kubelet hpk-pause	## Build HPK binary

build-race: ## Build HPK binary with race condition detector
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(VERSION_FLAGS) -race -o bin/hpk-kubelet ./cmd/hpk

hpk-kubelet:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(VERSION_FLAGS) -ldflags '-extldflags "-static"' -o bin/hpk-kubelet ./cmd/hpk

hpk-pause:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(VERSION_FLAGS) -ldflags '-extldflags "-static"' -o bin/hpk-pause ./cmd/pause

image-pause:
	DOCKER_BUILDKIT=1 docker build . -t $(PAUSE_IMAGE_TAG) -f deploy/images/pause-apptainer-agent/pause.apptainer.Dockerfile
	docker push $(PAUSE_IMAGE_TAG)

image-kubemaster: ## Build and push the Kubernetes Master image
	(cd k3s && DOCKER_BUILDKIT=1 docker build . -t $(K3S_IMAGE_TAG) -f Dockerfile)
	docker push $(K3S_IMAGE_TAG)

build-all: image-kubemaster image-pause build ## Build kubemaster and binaries

##@ Deployment

run-hpk-master:
	mkdir -p ${HPK_MASTER_PATH}/log
	apptainer run --net --dns ${EXTERNAL_DNS} --fakeroot \
	--cleanenv --pid --containall \
	--no-init --no-umask --no-eval \
	--no-mount tmp,home --unsquash --writable \
	--bind ${HPK_MASTER_PATH}:/usr/local/etc \
	--bind ${HPK_MASTER_PATH}/log:/var/log \
	docker://$(K3S_IMAGE_TAG)

run-kubelet: CA_BUNDLE = $(shell cat ${KUBE_PATH}/pki/ca.crt | base64 | tr -d '\n')
run-kubelet: HOST_ADDRESS = $(shell ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')
run-kubelet: ## Run the HPK Virtual Kubelet
	@echo "===> Generate HPK Certificates <==="
	mkdir -p ./bin

	if [ ! -f bin/kubelet.key ]; then openssl genrsa -out bin/kubelet.key 2048; fi

	echo "$$CERTIFICATE_CONFIGURATION" > bin/kubelet.cnf
	openssl req -new -key bin/kubelet.key -subj "/CN=hpk-kubelet" \
	-out bin/kubelet.csr -config bin/kubelet.cnf
	openssl x509 -req -days 365 -set_serial 01 \
	-CA ${KUBE_PATH}/pki/ca.crt -CAkey ${KUBE_PATH}/pki/ca.key \
	-in bin/kubelet.csr -out bin/kubelet.crt \
	-extfile bin/kubelet.cnf -extensions v3_req

	@echo "===> Register Webhook <==="
	export KUBECONFIG=${KUBE_PATH}/admin.conf; \
	echo "$$WEBHOOK_CONFIGURATION" | kubectl apply -f -

	@echo "===> Run HPK <==="
	KUBECONFIG=${KUBE_PATH}/admin.conf \
	APISERVER_KEY_LOCATION=bin/kubelet.key \
	APISERVER_CERT_LOCATION=bin/kubelet.crt \
	VKUBELET_ADDRESS=${HOST_ADDRESS} \
	./bin/hpk-kubelet

##@ Test

.PHONY: test
test: ## Run all tests
	if [ ! -d test/helper ]; then \
		mkdir test/helper; \
		git clone https://github.com/bats-core/bats-core.git test/helper/bats; \
		git clone https://github.com/bats-core/bats-support.git test/helper/bats-support; \
		git clone https://github.com/bats-core/bats-assert.git test/helper/bats-assert; \
		git clone https://github.com/bats-core/bats-detik.git test/helper/bats-detik; \
	fi
	export KUBECONFIG=${KUBE_PATH}/admin.conf; \
	./test/helper/bats/bin/bats test/test.bats

#.PHONY: build
#build: clean bin/hpk-kubelet

#.PHONY: clean
#clean: files := bin/hpk-kubelet
#clean:
#	@${RM} $(files) &>/dev/null || exit 0

#.PHONY: mod
#mod:
#	@go mod tidy
