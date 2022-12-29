FROM ubuntu:latest

RUN apt-get update && apt-get install -y wget

RUN wget https://github.com/apptainer/apptainer/releases/download/v1.1.4/apptainer_1.1.4_amd64.deb

RUN apt-get install -y ./apptainer_1.1.4_amd64.deb

RUN apt-get install -y iptables iproute2

RUN apt-get install -y fakeroot fakeroot-ng gosu 

RUN apt-get install -y patchelf

COPY docker-entrypoint.sh /usr/local/bin/
RUN ln -s usr/local/bin/docker-entrypoint.sh /entrypoint.sh # backwards compat

USER root
WORKDIR /root

ENTRYPOINT ["/entrypoint.sh"]
