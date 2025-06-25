# GossipSub Mininet Test

This project demonstrates a simple GossipSub implementation running on a Mininet network with 5 nodes.

## Prerequisites

- Go 1.21 or later
- Mininet
- Python 3

## Building and Running

1. First, get the dependencies:
```bash
go mod download
```

2. Run the Mininet topology:
```bash
sudo python3 topo.py
```

## How it Works

The topology consists of 5 hosts connected through a central router. Each host runs a GossipSub node:

- The first node (h1) will publish a message
- All other nodes (h2-h5) will receive and relay the message

The nodes will automatically connect to each other and form a GossipSub mesh network. The first node will publish a message after 5 seconds, which should be received by all other nodes.

## Monitoring

Each node's output is redirected to a log file in the `logs` directory. To monitor the messages:

1. View logs for a specific node:
```bash
tail -f logs/node1.log  # For node 1
tail -f logs/node2.log  # For node 2
# etc...
```

2. You can also monitor multiple nodes simultaneously:
```bash
tail -f logs/node*.log
```

## Cleanup

To clean up the processes:
```bash
pkill gossipsub
``` 