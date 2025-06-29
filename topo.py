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

class LinuxRouter(Node):
    def config(self, **params):
        super(LinuxRouter, self).config(**params)
        self.cmd('sysctl net.ipv4.ip_forward=1')

    def terminate(self):
        self.cmd('sysctl net.ipv4.ip_forward=0')
        super(LinuxRouter, self).terminate()

# Load ping data from CSV
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

class GossipSubTopo(Topo):
    def build(self, selected_nodes):
        # Create hosts
        for node in selected_nodes:
            self.addHost(f'h{node}', ip=f'10.0.{node}.1/24')
        # Create a router
        self.addNode('r1', cls=LinuxRouter, ip=f'10.0.{selected_nodes[0]}.2/24')
        # Add links
        for node in selected_nodes:
            self.addLink(f'h{node}', 'r1', intfName=f'h{node}-eth0', params1={'ip': f'10.0.{node}.1/24'})

def get_peer_ids(max_node):
    # Generate new keys and get peer IDs for all possible nodes
    result = subprocess.run(['go', 'run', 'main.go', '-generate', '-node', str(max_node)], capture_output=True, text=True)
    if result.returncode != 0:
        print("Error generating keys:", result.stderr)
        return None
    peer_ids = {}
    for line in result.stdout.splitlines():
        if match := re.match(r'Node (\d+) peer ID: (.+)$', line):
            node_num = int(match.group(1))
            peer_id = match.group(2)
            peer_ids[node_num] = peer_id
    return peer_ids


def run():
    # Delete all logs before starting
    os.system('rm -f logs/*.log')

    # Load ping data
    ping_data = pings_csv_to_dict("../pings.csv")
    
    # Read selected nodes from participants.txt
    selected_nodes = []
    try:
        with open("participants.txt", "r") as file:
            for line in file:
                line = line.strip()
                if line:  # Skip empty lines
                    selected_nodes.append(int(line))
    except FileNotFoundError:
        print("Error: participants.txt not found")
        return
    except ValueError as e:
        print(f"Error parsing participants.txt: {e}")
        return
    
    if not selected_nodes:
        print("Error: No nodes found in participants.txt")
        return
    
    print(f"Selected nodes from participants.txt: {selected_nodes}")

    # Get peer IDs for all possible nodes
    peer_ids = get_peer_ids(max(selected_nodes))
    if not peer_ids:
        print("Failed to get peer IDs")
        return

    # Build reduced ping matrix for selected nodes
    delays = {}
    for i in selected_nodes:
        for j in selected_nodes:
            if i != j and i in ping_data and j in ping_data[i]:
                # Use ping average as delay in ms (rounded)
                delays[(i, j)] = int(round(ping_data[i][j][0]))
            elif i != j:
                # Default delay if missing
                delays[(i, j)] = 20

    random.shuffle(selected_nodes)
    topo = GossipSubTopo(selected_nodes)
    net = Mininet(topo=topo)
    net.start()

    # Map node number to its index for interface and marking
    node_to_idx = {node: idx for idx, node in enumerate(selected_nodes)}

    # Configure router interfaces
    r1 = net['r1']
    for idx, node in enumerate(selected_nodes[1:], 1):
        r1.cmd(f'ip addr add 10.0.{node}.2/24 dev r1-eth{idx}')

    # Add routes
    for idx, node in enumerate(selected_nodes):
        net[f'h{node}'].cmd(f'ip route add default via 10.0.{node}.2')

    # Remove all per-router-band tc/iptables logic

    # Apply scalable per-destination delays on each host's interface using HTB + netem
    for i in selected_nodes:
        host = net[f'h{i}']
        intf = f'h{i}-eth0'
        host.cmd(f'tc qdisc del dev {intf} root || true')
        # Add HTB root qdisc
        host.cmd(f'tc qdisc add dev {intf} root handle 1: htb default 1')
        # Add a parent class (for default traffic)
        host.cmd(f'tc class add dev {intf} parent 1: classid 1:1 htb rate 1000Mbps')
        for j_idx, j in enumerate(selected_nodes):
            if i == j:
                continue
            classid = j_idx + 2  # 1:1 is default, so start at 1:2
            delay = delays[(i, j)]
            # Add a class for this destination
            host.cmd(f'tc class add dev {intf} parent 1: classid 1:{classid} htb rate 1000Mbps')
            # Attach netem to this class
            host.cmd(f'tc qdisc add dev {intf} parent 1:{classid} handle {classid}0: netem delay {delay}ms')
            # Add a filter to match destination IP
            host.cmd(f'tc filter add dev {intf} protocol ip parent 1: prio 1 u32 match ip dst 10.0.{j}.1 flowid 1:{classid}')

    os.system('mkdir -p logs')
    time.sleep(5)

    min_node = min(selected_nodes)
    # Start all nodes with their peer connections
    for idx, i in enumerate(selected_nodes):
        peer_list = []
        for j in selected_nodes:
            if j != i:
                peer_port = 4000 + j
                peer_list.append(f"/ip4/10.0.{j}.1/tcp/{peer_port}/p2p/{peer_ids[j]}")
        peers_arg = ','.join(peer_list)
        node_port = 4000 + i
        net[f'h{i}'].cmd(f'go run main.go -port {node_port} -node {i} -minnode {min_node} -peers "{peers_arg}" > logs/node{i}.log 2>&1 &')
        print(f"Started node {i}")

    print("\nTo view logs, use: tail -f logs/node<N>.log")
    print(f"Example: tail -f logs/node{selected_nodes[0]}.log\n")

    CLI(net)
    net.stop()

if __name__ == '__main__':
    setLogLevel('info')
    run() 