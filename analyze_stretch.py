import os
import re
import glob
from datetime import datetime
import csv

def parse_timestamp(line):
    match = re.match(r"\[(.*?)\]", line)
    if not match:
        return None
    try:
        ts = match.group(1)
        if "." in ts:
            base, frac = ts.split(".")
            frac = (frac + "000000")[:6]
            ts = f"{base}.{frac}"
        return datetime.fromisoformat(ts)
    except Exception:
        print("Could not parse timestamp", line)
        return None

def get_peer_id_from_publish(logfile):
    with open(logfile) as f:
        for line in f:
            if "ID:" in line:
                m = re.search(r"ID: (\S+)", line)
                if m:
                    return m.group(1)
    return None

def get_publish_time(logfile):
    with open(logfile) as f:
        for line in f:
            if "published message to topic" in line:
                return parse_timestamp(line)
    return None

def get_receive_time(logfile):
    with open(logfile) as f:
        for line in f:
            if "Received message from" in line:
                ts = parse_timestamp(line)
                if ts:
                    return ts
    return None

def load_pings(pingfile):
    pings = {}
    with open(pingfile, newline='') as csvfile:
        reader = csv.reader(csvfile)
        next(reader)
        for row in reader:
            src = int(row[0].replace('"', ''))
            dst = int(row[1].replace('"', ''))
            ping_avg = float(row[4].replace('"', ''))
            if src not in pings:
                pings[src] = {}
            pings[src][dst] = ping_avg
    return pings

def main():
    logs_dir = "logs"
    pingfile = "../pings.csv"
    logs = glob.glob(os.path.join(logs_dir, "node*.log"))
    pings = load_pings(pingfile)

    source_node = None
    source_publish_time = None
    source_peer_id = None
    for log in logs:
        t = get_publish_time(log)
        if t:
            source_node = int(re.search(r"node(\d+)\.log", log).group(1))
            source_publish_time = t
            source_peer_id = get_peer_id_from_publish(log)
            break

    if not source_node or not source_publish_time or not source_peer_id:
        print("Could not determine source node or publish time.")
        return

    stretches = []
    for log in logs:
        node = int(re.search(r"node(\d+)\.log", log).group(1))
        if node == source_node:
            continue
        receive_time = get_receive_time(log)
        if not receive_time:
            print(f"Node {node} did not receive the message")
            continue
        try:
            ping = pings[source_node][node]
        except KeyError:
            print(f"No ping data for {source_node}->{node}")
            continue
        stretch = (receive_time - source_publish_time).total_seconds() * 1000 / ping
        stretches.append(stretch)
        print(f"Node {node}: stretch={stretch:.2f} (delay={((receive_time - source_publish_time).total_seconds()*1000):.2f} ms, ping={ping} ms)")

    if stretches:
        avg_stretch = sum(stretches) / len(stretches)
        print(f"\nAverage stretch: {avg_stretch:.2f}")
    else:
        print("No stretches calculated.")

if __name__ == "__main__":
    main() 