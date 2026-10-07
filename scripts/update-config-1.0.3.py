#!/usr/bin/env python3
"""Create an updated copy of the existing config; never overwrite the input."""
import argparse
import json
import os
from pathlib import Path

parser = argparse.ArgumentParser(description="SmartHome 1.0.3: update siren commands and retained leak rules")
parser.add_argument("input", type=Path)
parser.add_argument("output", type=Path)
args = parser.parse_args()
config = json.loads(args.input.read_text(encoding="utf-8"))
changed = 0
for device in config.get("devices", []):
    if device.get("id") != "siren01":
        continue
    for action in device.get("actions", {}).values():
        for publish in action.get("publishes", []):
            payload = publish.get("payload")
            if isinstance(payload, dict) and type(payload.get("alarm")) in (int, float) and payload["alarm"] in (0, 1):
                payload["alarm"] = bool(payload["alarm"])
                changed += 1
for event in config.get("events", []):
    if event.get("when", {}).get("water_leak") is True and event.get("actions") and "allow_retained" not in event:
        event["allow_retained"] = True
        changed += 1
# These mappings are the valve/alarm groups in the supplied household config.
groups = {group.get("id"): group for group in config.get("groups", [])}
for valve_id, group_id in [("voda_bathroom", "bathroom_water_alarm"), ("voda_kitchen", "kitchen_water_alarm")]:
    group = groups.get(group_id, {})
    for device in config.get("devices", []):
        if device.get("id") != valve_id or "on" not in device.get("actions", {}):
            continue
        guards = device["actions"]["on"].setdefault("block_when", [])
        for sensor in group.get("members", []):
            guard = {"device_id": sensor, "field": "water_leak", "value": True}
            if guard not in guards:
                guards.append(guard)
                changed += 1
data = (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
try:
    with os.fdopen(fd, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
except BaseException:
    args.output.unlink(missing_ok=True)
    raise
print(f"Created {args.output}; changed {changed} entries; original configuration preserved.")
print("The new file is private (0600, current user). The service runs as user 'smarthome',")
print("so install it with read access for that group, for example:")
print(f"  sudo install -m 0640 -o root -g smarthome {args.output} /etc/smarthome.conf")
