FROM golang:1.18.7 AS builder

WORKDIR /go/src

COPY services-webhook /go/src/services-webhook
RUN (cd services-webhook && go build)

COPY random-scheduler /go/src/random-scheduler
RUN (cd random-scheduler && go build)

FROM ubuntu:22.04


ENV DEBIAN_FRONTEND=noninteractive
ENV K3S_TOKEN=secret
ENV K3S_KUBECONFIG_OUTPUT=/output/kubeconfig.yaml

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
    
RUN curl -sfL https://github.com/k3s-io/k3s/releases/download/v1.29.1+k3s1/k3s -o /usr/local/bin/k3s && \
    chmod +x /usr/local/bin/k3s
    
RUN mkdir -p /var/lib/rancher/k3s /etc/rancher/k3s

ARG TARGETARCH

# CoreDNS
ARG COREDNS_VERSION=1.10.0
RUN curl -LO https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz && \
    tar -zxvf coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz && \
    cp coredns /usr/local/bin/ && \
    rm -rf coredns coredns_${COREDNS_VERSION}_linux_${TARGETARCH}.tgz

RUN ["mkdir", "/output"]
RUN ["chmod", "777", "/output"]
VOLUME ["/output"]

COPY --from=builder /go/src/services-webhook/services-webhook /usr/local/bin/
COPY --from=builder /go/src/random-scheduler/random-scheduler /usr/local/bin/

COPY start-kubernetes.sh /usr/local/bin/

ENV K8SFS_HEADLESS_SERVICES=1
ENV K8SFS_RANDOM_SCHEDULER=1
ENV K8SFS_MOCK_KUBELET=1

CMD /usr/local/bin/start-kubernetes.sh

# CMD ["/bin/sh", "-c", "\
#     k3s server \
#       --disable-agent \
#       --disable scheduler \
#       --write-kubeconfig /output/kubeconfig.yaml \
#       --tls-san 127.0.0.1 \
#     "]