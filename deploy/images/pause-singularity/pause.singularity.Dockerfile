FROM ubuntu:jammy

RUN apt-get update; apt-get install -y wget


ENV VERSION 3.10.5

# Download the singularity binary
RUN wget https://github.com/sylabs/singularity/releases/download/v${VERSION}/singularity-ce_${VERSION}-jammy_amd64.deb

# Install the singularity binary
RUN apt-get install -fy ./singularity-ce_${VERSION}-jammy_amd64.deb && rm ./singularity-ce_${VERSION}-jammy_amd64.deb

# Install some generic stuff
RUN apt-get install -y iptables iproute2 fakeroot fakeroot-ng gosu patchelf vim squashfuse fuse uidmap squashfs-tools

#COPY docker-entrypoint.sh /usr/local/bin/
#RUN ln -s usr/local/bin/docker-entrypoint.sh /entrypoint.sh # backwards compat

COPY singularity.conf /etc/singularity/singularity.conf

# Clean up cache to remove image size
RUN apt-get clean --dry-run

#ENTRYPOINT ["/entrypoint.sh"]
