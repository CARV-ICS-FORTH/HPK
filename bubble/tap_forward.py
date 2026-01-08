#!/usr/bin/env python3

import os
import socket
import struct
import argparse
import threading
from fcntl import ioctl

# TUN/TAP Constants for Linux
TUNSETIFF = 0x400454ca
IFF_TAP   = 0x0002
IFF_NO_PI = 0x1000

def format_mac(data):
    """Formats 6 bytes into a colon-separated MAC string."""
    return ":".join(f"{b:02x}" for b in data)

def log_packet(data, label, verbose):
    if not verbose:
        return

    pkt_len = len(data)
    # Ethernet header is 14 bytes: [Dest MAC (6)][Src MAC (6)][EtherType (2)]
    if pkt_len < 14:
        print(f"[ {label} ] Packet too short ({pkt_len} bytes)")
        return

    eth_hdr = struct.unpack("!6s6sH", data[:14])
    eth_proto = eth_hdr[2]

    # Initialize basic log line
    log_msg = f"[ {label} ] Len: {pkt_len} | Eth: {format_mac(eth_hdr[1])} -> {format_mac(eth_hdr[0])}"

    # Handle IPv4 (EtherType 0x0800)
    if eth_proto == 0x0800 and pkt_len >= 34:
        # IPv4 header starts at byte 14.
        # Format: !BBHHHBBH4s4s (Network byte order, 20 bytes total)
        # index 6 = Protocol (1=ICMP, 6=TCP, 17=UDP), index 8 = Src IP, index 9 = Dst IP
        ip_hdr = struct.unpack("!BBHHHBBH4s4s", data[14:34])

        src_ip = socket.inet_ntoa(ip_hdr[8])
        dst_ip = socket.inet_ntoa(ip_hdr[9])
        proto_num = ip_hdr[6]

        # Map common protocol numbers to names
        proto_map = {1: "ICMP", 6: "TCP", 17: "UDP"}
        proto_name = proto_map.get(proto_num, str(proto_num))

        log_msg += f" | IP: {src_ip} -> {dst_ip} | Proto: {proto_name}"
    else:
        log_msg += f" | Type: 0x{eth_proto:04x}"

    print(log_msg)

def open_tap(name):
    # Strip path if user accidentally provides it (e.g., /dev/tap2 -> tap2)
    name = os.path.basename(name)
    
    # 1. Open the TUN/TAP clone device
    tap = os.open("/dev/net/tun", os.O_RDWR)
    
    # 2. Prepare the ifreq struct: 16 bytes for name, 2 bytes for flags
    # We pad the struct to ensure it is the size the kernel expects (often 40 bytes)
    ifr = struct.pack("16sH", name.encode('utf-8'), IFF_TAP | IFF_NO_PI)
    ifr += b'\x00' * 22 # Padding to reach ifreq size
    
    try:
        ioctl(tap, TUNSETIFF, ifr)
    except OSError as e:
        print(f"Error: Could not setup TAP {name}. Are you root?")
        raise e
    
    print(f"[*] TAP interface '{name}' is ready.")
    return tap

def setup_unix_socket(path, mode):
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    if mode == 'serve':
        if os.path.exists(path): os.remove(path)
        sock.bind(path)
        sock.listen(1)
        print(f"[*] Serving on {path}...")
        conn, _ = sock.accept()
        return conn
    else:
        print(f"[*] Connecting to {path}...")
        sock.connect(path)
        return sock

def bridge_loop(tap_fd, unix_sock, verbose):
    def tap_to_unix():
        while True:
            try:
                packet = os.read(tap_fd, 2048)
                if not packet: break
                log_packet(packet, "TAP -> UNIX", verbose)
                # Prefix with 2-byte length (Network Byte Order)
                header = struct.pack("!H", len(packet))
                unix_sock.sendall(header + packet)
            except Exception as e: break

    def unix_to_tap():
        while True:
            try:
                # 1. Read the 2-byte length prefix
                length_data = unix_sock.recv(2)
                if not length_data: break
                packet_len = struct.unpack("!H", length_data)[0]

                # 2. Read exactly packet_len bytes (handle partial reads)
                packet = b""
                while len(packet) < packet_len:
                    chunk = unix_sock.recv(packet_len - len(packet))
                    if not chunk: break
                    packet += chunk

                log_packet(packet, "UNIX -> TAP", verbose)
                os.write(tap_fd, packet)
            except Exception as e: break

    threading.Thread(target=tap_to_unix, daemon=True).start()
    unix_to_tap()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--tap", required=True, help="TAP name (e.g. tap0)")
    parser.add_argument("--socket", required=True)
    parser.add_argument("--mode", choices=['serve', 'connect'], default='serve')
    parser.add_argument("-v", "--verbose", action="store_true")
    args = parser.parse_args()

    try:
        fd = open_tap(args.tap)
        sk = setup_unix_socket(args.socket, args.mode)
        bridge_loop(fd, sk, args.verbose)
    except KeyboardInterrupt:
        pass
