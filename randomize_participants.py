#!/usr/bin/env python3

import random
import csv
import os

def pings_csv_to_dict(filename: str) -> dict[int, dict[int, tuple[float, float]]]:
    data = {}
    with open(filename, "r") as file:
        next(file)  # Skip header
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

def randomize_participants():
    PERCENTAGE = 0.1
    MIN_NODES = 2
    OUTPUT_FILE = "participants.txt"
    
    ping_data = pings_csv_to_dict("../pings.csv")
    all_nodes = sorted(set(ping_data.keys()) | {d for v in ping_data.values() for d in v})
    
    num_nodes = max(MIN_NODES, int(len(all_nodes) * PERCENTAGE))
    
    if num_nodes > len(all_nodes):
        num_nodes = len(all_nodes)
    
    selected_nodes = sorted(random.sample(all_nodes, num_nodes))
    
    try:
        with open(OUTPUT_FILE, 'w') as f:
            for node in selected_nodes:
                f.write(f"{node}\n")        
        
    except Exception as e:
        print(f"Error writing to output file: {e}")

if __name__ == '__main__':
    randomize_participants()
