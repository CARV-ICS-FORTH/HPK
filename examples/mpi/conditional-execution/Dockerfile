FROM pawsey/openmpi-base:2.1.2_ubuntu20.04

RUN apt-get update &&  apt-get install -y openssh-server

# mpi-operator mounts the .ssh folder from a Secret. For that to work, we need
# to disable UserKnownHostsFile to avoid write permissions.
# Disabling StrictModes avoids directory and files read permission checks.
RUN echo "    UserKnownHostsFile /dev/null" >> /etc/ssh/ssh_config && \
    sed -i 's/#\(StrictModes \).*/\1no/g' /etc/ssh/sshd_config


# https://github.com/microsoft/WSL/issues/3621
RUN ssh-keygen -A && mkdir -p /run/sshd
