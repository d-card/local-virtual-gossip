#!/usr/bin/env python3

from mininet.topo import Topo
from mininet.net import Mininet
from mininet.node import Node
from mininet.log import setLogLevel, info
from mininet.cli import CLI
import time
import os
import re
import subprocess
import random
import csv
import argparse


class LinuxRouter(Node):
    def config(self, **params):
        super(LinuxRouter, self).config(**params)
        self.cmd("sysctl net.ipv4.ip_forward=1")

    def terminate(self):
        self.cmd("sysctl net.ipv4.ip_forward=0")
        super(LinuxRouter, self).terminate()


def pings_csv_to_dict(filename: str) -> dict[int, dict[int, tuple[float, float]]]:
    data = {}
    with open(filename, "r") as file:
        next(file)
        for line in file:
            parts = [part.strip() for part in line.split(",")]
            source = int(parts[0].replace('"', ""))
            destination = int(parts[1].replace('"', ""))
            ping_avg = float(parts[4].replace('"', ""))
            ping_std_dev = float(parts[6].replace('"', ""))
            if source not in data:
                data[source] = {}
            data[source][destination] = (ping_avg, ping_std_dev)
    return data


def get_ip_for_node(node: int) -> tuple[str, str]:
    """Generate IP address for a node, handling octets > 255"""
    subnet = (node // 255) + 1
    octet = (node % 255) + 1
    return f"10.{subnet}.{octet}.1/24", f"10.{subnet}.{octet}.2/24"


class GossipSubTopo(Topo):
    def build(self, selected_nodes):
        for node in selected_nodes:
            host_ip, _ = get_ip_for_node(node)
            self.addHost(f"h{node}", ip=host_ip)

        # Router gets IP from first node
        _, router_ip = get_ip_for_node(selected_nodes[0])
        self.addNode("r1", cls=LinuxRouter, ip=router_ip)

        for node in selected_nodes:
            host_ip, _ = get_ip_for_node(node)
            self.addLink(
                f"h{node}", "r1", intfName=f"h{node}-eth0", params1={"ip": host_ip}
            )


def get_peer_ids(max_node, binary_path):
    result = subprocess.run(
        [binary_path, "-generate", "-node", str(max_node)],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print("Error generating keys:", result.stderr)
        return None
    peer_ids = {}
    for line in result.stdout.splitlines():
        if match := re.match(r"(\d+):(.+)$", line):
            node_num = int(match.group(1))
            peer_id = match.group(2)
            peer_ids[node_num] = peer_id
    return peer_ids


def run(binary_path):
    print("[INFO] Cleaning logs...")
    os.system("rm -f logs/*.log")
    os.makedirs("logs", exist_ok=True)

    print("[INFO] Loading ping data...")
    ping_data = pings_csv_to_dict("../pings.csv")

    selected_nodes = []
    print("[INFO] Reading participants.txt...")
    try:
        with open("participants.txt", "r") as file:
            for line in file:
                line = line.strip()
                if line:
                    selected_nodes.append(int(line))
    except Exception as e:
        print("Error reading participants.txt:", e)
        return

    if not selected_nodes:
        print("No nodes selected.")
        return

    print(f"[INFO] Selected nodes: {selected_nodes}")

    print("[INFO] Generating peer IDs...")
    peer_ids = get_peer_ids(max(selected_nodes), binary_path)
    if not peer_ids:
        print("Failed to get peer IDs")
        return

    print("[INFO] Building delay matrix...")
    delays = {}
    for i in selected_nodes:
        for j in selected_nodes:
            if i != j and i in ping_data and j in ping_data[i]:
                delays[(i, j)] = int(round(ping_data[i][j][0]))
            elif i != j:
                delays[(i, j)] = 20

    print("[INFO] Shuffling and initializing topology...")
    random.shuffle(selected_nodes)
    topo = GossipSubTopo(selected_nodes)
    net = Mininet(topo=topo)
    net.start()

    print("[INFO] Setting up router interfaces...")
    r1 = net["r1"]
    for idx, node in enumerate(selected_nodes[1:], 1):
        _, router_ip = get_ip_for_node(node)
        r1.cmd(f"ip addr add {router_ip} dev r1-eth{idx}")

    print("[INFO] Setting up default routes...")
    for node in selected_nodes:
        _, router_ip = get_ip_for_node(node)
        router_ip_base = router_ip.split("/")[0]
        net[f"h{node}"].cmd(f"ip route add default via {router_ip_base}")

    print("[INFO] Applying per-host delay configuration...")
    for i in selected_nodes:
        print(f"[INFO] Setting up delay for node {i}...")
        host = net[f"h{i}"]
        intf = f"h{i}-eth0"
        host.cmd(f"tc qdisc del dev {intf} root || true")
        host.cmd(f"tc qdisc add dev {intf} root handle 1: htb default 1")
        host.cmd(f"tc class add dev {intf} parent 1: classid 1:1 htb rate 1000Mbps")
        class_counter = 2
        for j in selected_nodes:
            if i == j:
                continue
            delay = delays[(i, j)]
            host_ip, _ = get_ip_for_node(j)
            host_ip_base = host_ip.split("/")[0]
            host.cmd(
                f"tc class add dev {intf} parent 1: classid 1:{class_counter} htb rate 1000Mbps"
            )
            host.cmd(
                f"tc qdisc add dev {intf} parent 1:{class_counter} handle {class_counter}0: netem delay {delay}ms"
            )
            host.cmd(
                f"tc filter add dev {intf} protocol ip parent 1: prio 1 u32 match ip dst {host_ip_base} flowid 1:{class_counter}"
            )
            class_counter += 1

    print("[INFO] Delay configuration complete. Waiting before launch...")
    time.sleep(2)

    print("[INFO] Launching node processes...")
    min_node = min(selected_nodes)
    log_files = {}
    for i in selected_nodes:
        log_path = f"logs/node{i}.log"
        log_file = open(log_path, "w", buffering=1)  # Line-buffered
        log_files[i] = log_file

        peer_list = []
        for j in selected_nodes:
            if j != i:
                peer_port = 4000 + j
                host_ip, _ = get_ip_for_node(j)
                host_ip_base = host_ip.split("/")[0]
                peer_list.append(
                    f"/ip4/{host_ip_base}/tcp/{peer_port}/p2p/{peer_ids[j]}"
                )
        peers_arg = ",".join(peer_list)
        node_port = 4000 + i

        print(f"[INFO] Starting node {i} on port {node_port}...")
        net[f"h{i}"].popen(
            [
                binary_path,
                "-port",
                str(node_port),
                "-node",
                str(i),
                "-minnode",
                str(min_node),
                "-peers",
                peers_arg,
            ],
            stdout=log_file,
            stderr=subprocess.STDOUT,
        )

    print("\n[INFO] All nodes launched. View logs with:")
    print("tail -f logs/node<N>.log")
    print(f"Example: tail -f logs/node{selected_nodes[0]}.log\n")

    CLI(net)
    net.stop()

    for f in log_files.values():
        f.close()


if __name__ == "__main__":
    setLogLevel("info")
    parser = argparse.ArgumentParser(description="Mininet GossipSub Topology")
    parser.add_argument(
        "--binary",
        type=str,
        default="bin/node",
        help="Path to node binary (default: bin/node)",
    )
    args = parser.parse_args()
    run(args.binary)
