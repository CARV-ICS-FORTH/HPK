FROM golang:1.18.7 AS builder

WORKDIR /go/src

COPY services-webhook /go/src/services-webhook
RUN (cd services-webhook && go build)

COPY random-scheduler /go/src/random-scheduler
RUN (cd random-scheduler && go build)

FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y iproute2 curl openssl && \
    apt-get clean \
    && rm -rf \
        /var/lib/apt/lists/* \
        /tmp/* \
        /var/tmp/* \
        /usr/share/man \
        /usr/share/doc \
        /usr/share/doc-base

ARG TARGETARCH

# K3s
ARG K3S_VERSION=v1.29.1+k3s1

RUN set -eux; \
    if [ "${TARGETARCH}" = "amd64" ]; then \
        K3S_ARCH=""; \
    else \
        K3S_ARCH="-${TARGETARCH}"; \
    fi; \
    curl -sfL "https://github.com/k3s-io/k3s/releases/download/${K3S_VERSION}/k3s${K3S_ARCH}" -o /usr/local/bin/k3s && \
    chmod +x /usr/local/bin/k3s

RUN mkdir -p /var/lib/rancher/k3s /etc/rancher/k3s

# CoreDNS
ARG COREDNS_VERSION=1.10.0
RUN curl -LO https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz && \
    tar -zxvf coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz && \
    cp coredns /usr/local/bin/ && \
    rm -rf coredns coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz

COPY --from=builder /go/src/services-webhook/services-webhook /usr/local/bin/
COPY --from=builder /go/src/random-scheduler/random-scheduler /usr/local/bin/

COPY generate-keys.sh /usr/local/bin/
RUN chmod 777 /usr/local/bin/generate-keys.sh
COPY start-kubernetes.sh /usr/local/bin/

CMD /usr/local/bin/start-kubernetes.sh
