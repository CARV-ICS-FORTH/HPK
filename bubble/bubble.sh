#!/bin/bash -x

BUBBLE_ID=${1:-1}

NAME=bubble$BUBBLE_ID

CIDR=10.0.$((BUBBLE_ID + 1)).0
CIDR_PREFIX=$(echo "$CIDR" | awk -F. '{print $1"."$2"."$3}')
DNS_ADDR=$CIDR_PREFIX.3
NS_ADDR=$CIDR_PREFIX.100

cleanup() {
	echo "Cleaning up..."
	if [[ -n $SLIRP_PID ]]; then
		kill $SLIRP_PID 2>/dev/null
		wait $SLIRP_PID 2>/dev/null
	fi
	apptainer instance stop $NAME
	[ -e $NAME-slirp4netns.sock ] && rm -f $NAME-slirp4netns.sock
}

trap cleanup INT TERM

# Namespace
[ -f resolv.conf ] || echo "nameserver $DNS_ADDR" > resolv.conf
apptainer instance run \
	--fakeroot \
	--no-mount home \
	--no-mount cwd \
	--no-mount tmp \
	--no-mount hostfs \
	--writable-tmpfs \
	--network=none \
	--bind resolv.conf:/etc/resolv.conf \
	docker://chazapis/hpk-bubble:4 \
	$NAME
PID=$(apptainer instance list -j $NAME | jq -r '.instances[] | .pid')

# Userlevel networking
slirp4netns --configure --cidr=$CIDR/24 --mtu=65520 --api-socket $NAME-slirp4netns.sock $PID tap0 &
SLIRP_PID=$!

while [ ! -e $NAME-slirp4netns.sock ]; do
    sleep 1
done
echo -n '{"execute": "add_hostfwd", "arguments": {"proto": "udp", "host_addr": "0.0.0.0", "host_port": 8472, "guest_addr": "'$NS_ADDR'", "guest_port": 8472}}' | nc -U $NAME-slirp4netns.sock

wait $SLIRP_PID
