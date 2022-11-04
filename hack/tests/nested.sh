#!/bin/bash
set -eum pipeline

#
# Pod (Nested Apptainer) Level
#
cat <<EOF > /tmp/start_nested_pod.sh
  echo "Starting Pod-Level Operations"

  # Remove Apptainer/Singularity flags.
  # If not removed, they will be consumed by the nested apptainer/singularity and overwrite paths.
  echo "Reset Apptainer/Singularity Flags"

  unset SINGULARITY_BIND
  unset SINGULARITY_CONTAINER
  unset SINGULARITY_ENVIRONMENT
  unset SINGULARITY_NAME
  unset APPTAINER_APPNAME
  unset APPTAINER_BIND
  unset APPTAINER_CONTAINER
  unset APPTAINER_ENVIRONMENT
  unset APPTAINER_NAME

  # Start the containers (as processes) within the Pod
  echo "Starting Iperf server"
  apptainer exec iperf2_latest.sif iperf -s &

  echo "Starting Iperf client"
  apptainer exec iperf2_latest.sif iperf -c localhost
EOF

chmod +x /tmp/start_nested_pod.sh

#
# Host Level
#
echo "Starting Host-Level Operations"

# Download images locally. This is a workaround to avoid DNS issues
apptainer pull --force docker://czero/iperf2
apptainer pull --force docker://godlovedc/lolcow

# Start a nested Pod environment in apptainer
echo "Starting Pod"

apptainer exec --net --fakeroot                                            \
--bind /bin,/etc/apptainer,/home,/lib,/lib32,/lib64,/libx32,/opt,/root,/sbin,/run,/sys,/usr,/var,/tmp   \
docker://alpine /tmp/start_nested_pod.sh
