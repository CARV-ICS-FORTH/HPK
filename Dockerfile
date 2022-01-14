FROM ubuntu:latest as builder
RUN apt-get update && apt-get install -y golang-go build-essential git
WORKDIR /build
COPY . .
RUN go mod tidy && go get
RUN make build

FROM ubuntu:latest
RUN apt-get update && apt-get install -y openssh-server sudo curl
RUN useradd --create-home --shell /bin/bash user0 && echo "user0:user0" | chpasswd && adduser user0 sudo && mkdir -p /home/user0/.ssh

WORKDIR /home/user0

ENV APISERVER_CERT_LOCATION /home/user0/knoc-crt.pem
ENV APISERVER_KEY_LOCATION /home/user0/knoc-key.pem
ENV KUBELET_PORT 10250

# Copy the configuration file for the knoc provider.
COPY --from=builder /build/deploy/knoc-cfg.json /home/user0/knoc-cfg.json
# Copy the certificate for the HTTPS server.
COPY --from=builder /build/deploy/knoc-crt.pem /home/user0/knoc-crt.pem
# Copy the private key for the HTTPS server.
COPY --from=builder /build/deploy/knoc-key.pem /home/user0/knoc-key.pem

COPY --from=builder /build/bin/virtual-kubelet /usr/local/bin/virtual-kubelet
COPY --from=builder /build/bin/door /usr/local/bin/door

RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
RUN chmod +x kubectl && \
    mv ./kubectl /usr/local/bin/kubectl

USER user0
CMD ["/usr/local/bin/virtual-kubelet"]
