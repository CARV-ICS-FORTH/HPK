#!/bin/bash
set -eum pipeline

#
# Host-Level
#
echo "Starting Host-Level Operations"

# Download images locally. This is a workaround to avoid DNS issues
apptainer pull --force docker://czero/iperf2
apptainer pull --force docker://godlovedc/lolcow

# Start an Iperf Pod environment in apptainer
echo "Starting Iperf server"
apptainer exec --net --fakeroot iperf2_latest.sif sh -c  "hostname -I; iperf -s" &


# Fancy prompty message
apptainer exec lolcow_latest.sif lolcat << EOF
"## Please give the server's IP ##"
EOF

read -p  'Server IP: ' serverIP

apptainer exec --net --fakeroot iperf2_latest.sif sh -c  "hostname -I; iperf -c ${serverIP}"
