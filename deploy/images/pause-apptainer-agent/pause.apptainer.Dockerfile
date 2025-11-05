FROM golang:latest AS builder

WORKDIR /app

# Copy your HPK-pause source code
COPY . .
# Build the HPK-pause binary
RUN make hpk-pause

# --- Minimal final image ---

FROM ubuntu:latest

ENV VERSION 1.1.9

RUN apt-get update && apt-get install -y wget

RUN wget https://github.com/apptainer/apptainer/releases/download/v${VERSION}/apptainer_${VERSION}_amd64.deb

RUN apt-get install -y ./apptainer_${VERSION}_amd64.deb && rm ./apptainer_${VERSION}_amd64.deb

# (Install other dependencies) 
RUN apt-get install -y iptables iproute2 fakeroot gosu patchelf nano vim

# Copy the compiled pause binary from the builder stage
COPY --from=builder /app/bin/hpk-pause /usr/local/bin
COPY --from=builder /app/deploy/images/pause-apptainer-agent/docker-entrypoint.sh /usr/local/bin/

RUN ln -s usr/local/bin/docker-entrypoint.sh /entrypoint.sh # backwards compat
USER root
WORKDIR /root

ENTRYPOINT ["/entrypoint.sh"]
