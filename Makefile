GO111MODULE := on
export GO111MODULE

.PHONY: build
build: clean bin/hpk-kubelet

.PHONY: clean
clean: files := bin/hpk-kubelet
clean:
	@${RM} $(files) &>/dev/null || exit 0

.PHONY: mod
mod:
	@go mod tidy

bin/hpk-kubelet: BUILD_VERSION  ?= $(shell git describe --tags --always --dirty="-dev")
bin/hpk-kubelet: BUILD_DATE     ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')
bin/hpk-kubelet: VERSION_FLAGS	:= -ldflags='-X "main.buildVersion=$(BUILD_VERSION)" -X "main.buildTime=$(BUILD_DATE)"'

bin/%:
# 	CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -o bin/$(*) $(VERSION_FLAGS) ./cmd/$(*)
	CGO_ENABLED=1 GOOS=linux go build -race -a -o bin/$(*) $(VERSION_FLAGS) ./cmd/$(*)
