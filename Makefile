LINTER_BIN ?= golangci-lint

GO111MODULE := on
export GO111MODULE
# include Makefile.e2e

.PHONY: build
build: clean bin/virtual-kubelet bin/door

.PHONY: door_clean clean

door_clean:
	@${RM} bin/door

bin/door: BUILD_VERSION          ?= $(shell git describe --tags --always --dirty="-dev")
bin/door: BUILD_DATE             ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
bin/door: VERSION_FLAGS    := -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'
bin/door: door/door.go door/types.go
	CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -o bin/door $(VERSION_FLAGS) door/door.go door/types.go

.PHONY: clean
clean: files := bin/virtual-kubelet
clean: door_clean
	@${RM} $(files) &>/dev/null || exit 0

.PHONY: test
test:
	@echo running tests
	go test -v ./...

.PHONY: vet
vet:
	@go vet ./... #$(packages)

.PHONY: lint
lint:
	@$(LINTER_BIN) run --new-from-rev "HEAD~$(git rev-list master.. --count)" ./...

.PHONY: check-mod
check-mod: # verifies that module changes for go.mod and go.sum are checked in
	@hack/ci/check_mods.sh

.PHONY: mod
mod:
	@go mod tidy

bin/virtual-kubelet: BUILD_VERSION          ?= $(shell git describe --tags --always --dirty="-dev")
bin/virtual-kubelet: BUILD_DATE             ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
bin/virtual-kubelet: VERSION_FLAGS    := -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'

bin/%:
	CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -o bin/$(*) $(VERSION_FLAGS) ./cmd/$(*)


# # skaffold deploys the virtual-kubelet to the Kubernetes cluster targeted by the current kubeconfig using skaffold.
# # The current context (as indicated by "kubectl config current-context") must be one of "minikube" or "docker-for-desktop".
# # MODE must be set to one of "dev" (default), "delete" or "run", and is used as the skaffold command to be run.
# .PHONY: skaffold
# skaffold: MODE ?= dev
# .SECONDEXPANSION:
# skaffold: skaffold/$$(MODE)

# .PHONY: skaffold/%
# skaffold/%: PROFILE := local
# skaffold/%: skaffold.validate
# 	skaffold $(*) \
# 		-f $(PWD)/deploy/skaffold.yml \
# 		-p $(PROFILE)

# skaffold/run skaffold/dev: bin/virtual-kubelet

# container: PROFILE := local
# container: skaffold.validate
# 	skaffold build --platform=linux/amd64 -f $(PWD)/deploy/skaffold.yml \
# 		-p $(PROFILE)