#!/bin/bash
set -eum pipeline

#
# Pod (Nested Singularity) Level
#
cat <<EOF > /tmp/start_intra_pod.sh
  echo "Starting Pod-Level Operations"

  # Remove Singularity/Apptainer flags.
  # If not removed, they will be consumed by the nested singularity and overwrite paths.
  echo "Reset Singularity/Apptainer Flags"

  unset SINGULARITY_BIND
  unset SINGULARITY_CONTAINER
  unset SINGULARITY_ENVIRONMENT
  unset SINGULARITY_NAME
  unset APPTAINER_APPNAME
  unset APPTAINER_BIND
  unset APPTAINER_CONTAINER
  unset APPTAINER_ENVIRONMENT
  unset APPTAINER_NAME


  echo Server Pod's IP: "$$(hostname -i)"

  # Start the containers (as processes) within the Pod
  echo "Starting Iperf server"
  apptainer exec iperf2_latest.sif iperf -s &

#  echo "Starting Iperf client"
#  apptainer exec iperf2_latest.sif iperf -c localhost &
EOF

chmod +x /tmp/start_intra_pod.sh


#
# Host Level
#
echo "Starting Host-Level Operations"

# Download images locally. This is a workaround to avoid DNS issues
apptainer pull --force docker://czero/iperf2
apptainer pull --force docker://godlovedc/lolcow

# Start a nested Pod environment in apptainer
echo "Starting Server Pod"

apptainer exec --net --network=flannel  --fakeroot                                            \
--bind /bin,/etc,/home,/lib,/lib32,/lib64,/libx32,/opt,/root,/sbin,/run,/sys,/usr,/var,/tmp   \
docker://icsforth/scratch                                                                     \
/tmp/start_intra_pod.sh



# Fancy prompty message
apptainer exec lolcow_latest.sif lolcat << EOF
"## Please give the server's IP ##"
EOF

read -p  'Server IP: ' serverIP

apptainer exec --net --network=flannel  --fakeroot iperf2_latest.sif sh -c  "hostname -I; iperf -c ${serverIP}"