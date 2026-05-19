#!/usr/bin/env python3
import json

print(json.dumps({
    "protocol": "udp",
    "local_ip": "192.168.1.2",
    "local_port": 51820,
    "public_ip": "203.0.113.10",
    "public_port": 45182,
}))
