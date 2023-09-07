FROM ubuntu:latest

ENV VERSION 1.1.9

RUN apt-get update && apt-get install -y wget

RUN wget https://github.com/apptainer/apptainer/releases/download/v${VERSION}/apptainer_${VERSION}_amd64.deb

RUN apt-get install -y ./apptainer_${VERSION}_amd64.deb && rm ./apptainer_${VERSION}_amd64.deb

RUN apt-get install -y iptables iproute2

RUN apt-get install -y fakeroot fakeroot-ng gosu 

RUN apt-get install -y patchelf nano vim

COPY docker-entrypoint.sh /usr/local/bin/
RUN ln -s usr/local/bin/docker-entrypoint.sh /entrypoint.sh # backwards compat

USER root
WORKDIR /root

ENTRYPOINT ["/entrypoint.sh"]
