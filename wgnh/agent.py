from __future__ import annotations

import argparse
import json
import platform
import subprocess
import time
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from . import __version__
from .config import load_json_config
from .protocol import json_dumps
from .wgconfig import apply_endpoint


class Agent:
    def __init__(self, config: dict[str, Any]):
        self.config = config
        self.node_id = config["node_id"]
        self.daemon_url = config["daemon_url"].rstrip("/")
        self.token = read_token(config)

    def run_forever(self) -> None:
        while True:
            try:
                command = self.poll()
                if command:
                    self.handle_command(command)
            except (HTTPError, URLError, OSError, ValueError) as exc:
                print(f"agent error: {exc}")
                time.sleep(int(self.config.get("retry_seconds", 5)))

    def poll(self) -> dict[str, Any] | None:
        meta = {
            "platform": platform.platform(),
            "agent_version": __version__,
        }
        response = self.post("/agent/poll", {"meta": meta}, timeout=35)
        return response.get("command")

    def handle_command(self, command: dict[str, Any]) -> None:
        command_id = command["command_id"]
        action = command["action"]
        payload = command.get("payload", {})
        try:
            if action == "natter.run":
                result = self.run_natter(payload)
                self.report("natter.result", result, command_id=None)
                self.report("action.result", {"ok": True, "detail": "natter completed"}, command_id=command_id)
                return

            if action == "endpoint.apply":
                result = self.apply_endpoint_command(payload)
                self.report(
                    "action.result",
                    {
                        "ok": True,
                        "binding_id": payload.get("binding_id", ""),
                        "changed": result.changed,
                        "message": result.message,
                    },
                    command_id=command_id,
                )
                return

            raise ValueError(f"unsupported action: {action}")
        except Exception as exc:
            self.report(
                "action.result",
                {"ok": False, "binding_id": payload.get("binding_id", ""), "error": str(exc)},
                command_id=command_id,
            )

    def run_natter(self, payload: dict[str, Any]) -> dict[str, Any]:
        natter = self.config.get("natter", {})
        interface = payload["server_interface"]
        iface_config = interface_config(self.config, interface)
        command = natter.get("command")
        if not command:
            raise ValueError("natter.command is not configured")
        args = list(command)
        proc = subprocess.run(
            args,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=int(natter.get("timeout_seconds", 90)),
        )
        result = parse_natter_stdout(proc.stdout)
        result["server_interface"] = interface
        result.setdefault("local_port", iface_config.get("listen_port", 0))
        return result

    def apply_endpoint_command(self, payload: dict[str, Any]):
        return apply_endpoint(
            config_type=payload["config_type"],
            interface=payload["interface"],
            peer_public_key=payload["peer_public_key"],
            endpoint_host=payload["endpoint_host"],
            endpoint_port=int(payload["endpoint_port"]),
            config_path=payload.get("config_path", ""),
            reload_method=payload.get("reload_method", "none"),
            dry_run=bool(self.config.get("dry_run", False)),
        )

    def report(self, report_type: str, payload: dict[str, Any], command_id: str | None = None) -> None:
        body: dict[str, Any] = {"type": report_type, "payload": payload}
        if command_id:
            body["command_id"] = command_id
        self.post("/agent/report", body, timeout=10)

    def post(self, path: str, body: dict[str, Any], timeout: int) -> dict[str, Any]:
        req = Request(
            self.daemon_url + path,
            data=json_dumps(body).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "X-Node-ID": self.node_id,
                "Authorization": f"Bearer {self.token}",
            },
            method="POST",
        )
        with urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))


def read_token(config: dict[str, Any]) -> str:
    if "token" in config:
        return str(config["token"])
    token_file = config.get("token_file")
    if not token_file:
        raise ValueError("token or token_file is required")
    with open(token_file, "r", encoding="utf-8") as fh:
        return fh.read().strip()


def interface_config(config: dict[str, Any], name: str) -> dict[str, Any]:
    for item in config.get("wireguard", []):
        if item.get("name") == name:
            return item
    raise ValueError(f"wireguard interface {name} is not configured")


def parse_natter_stdout(stdout: str) -> dict[str, Any]:
    for line in reversed(stdout.splitlines()):
        line = line.strip()
        if not line:
            continue
        try:
            data = json.loads(line)
        except json.JSONDecodeError:
            continue
        return {
            "protocol": data["protocol"],
            "local_ip": data.get("local_ip", ""),
            "local_port": int(data.get("local_port", 0)),
            "public_ip": data["public_ip"],
            "public_port": int(data["public_port"]),
        }
    raise ValueError("natter command must print a JSON result line")


def agent_cli(args: argparse.Namespace) -> None:
    config = load_json_config(args.config)
    Agent(config).run_forever()
