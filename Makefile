# go options

GO111MODULE := on
export GO111MODULE

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell git))
BUILD_VERSION=$(git describe --tags --always --dirty="-dev")
else
BUILD_VERSION='unknown'
endif

BUILD_DATE     ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
VERSION_FLAGS   := -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'


# deployment options
KUBEPATH ?= ${HOME}/.k8sfs/kubernetes/
HOST_ADDRESS ?= $(shell ip route get 1 | sed -n 's/.*src \([0-9.]\+\).*/\1/p')

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.DEFAULT_GOAL := help

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


##@ Build

build: ## Build HPK binary.
	GOOS=linux GOARCH=amd64 go build $(VERSION_FLAGS) -ldflags '-extldflags "-static"' -o bin/hpk-kubelet ./cmd/hpk

build-race: ## Build HPK binary with race condition detector.
	GOOS=linux GOARCH=amd64 go build $(VERSION_FLAGS) -race -o bin/hpk-kubelet ./cmd/hpk


##@ Deployment

run-kubemaster: ## Run the Kubernetes Master
	mkdir -p .k8sfs

	apptainer run --net --dns 8.8.8.8 --fakeroot \
    --cleanenv --pid --containall \
    --no-init --no-umask --no-eval \
    --no-mount tmp,home --unsquash --writable \
    --env K8SFS_MOCK_KUBELET=0 \
    --bind .k8sfs:/usr/local/etc \
    docker://chazapis/kubernetes-from-scratch:20221206


run-kubelet: ## Run the the Kubernetes virtual kubelet
	echo "===> Generate HPK Certificates <==="

	mkdir -p ./bin

	openssl genrsa -out bin/kubelet.key 2048
	openssl req -x509 -key bin/kubelet.key -CA ${KUBEPATH}/pki/ca.crt -CAkey ${KUBEPATH}/pki/ca.key \
        -days 365 -nodes -out bin/kubelet.crt -subj "/CN=hpk-kubelet" \
        -addext "basicConstraints=CA:FALSE" \
        -addext "keyUsage=digitalSignature,keyEncipherment" \
        -addext "extendedKeyUsage=serverAuth,clientAuth" \
        -addext "subjectAltName=IP:127.0.0.1,IP:${HOST_ADDRESS}"

	echo "===> Run HPK <==="
	KUBECONFIG=${KUBEPATH}/admin.conf				\
	APISERVER_KEY_LOCATION=bin/kubelet.key          \
	APISERVER_CERT_LOCATION=bin/kubelet.crt         \
	VKUBELET_ADDRESS=${HOST_ADDRESS}                \
	./bin/hpk-kubelet




#.PHONY: build
#build: clean bin/hpk-kubelet

#.PHONY: clean
#clean: files := bin/hpk-kubelet
#clean:
#	@${RM} $(files) &>/dev/null || exit 0

#.PHONY: mod
#mod:
#	@go mod tidy
