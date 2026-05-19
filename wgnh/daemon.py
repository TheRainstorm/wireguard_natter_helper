from __future__ import annotations

import argparse
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, urlparse

from .auth import generate_token
from .protocol import AgentCommand, json_dumps, json_loads
from .store import Store


class DaemonState:
    def __init__(self, store: Store, poll_seconds: int = 25, admin_token: str = ""):
        self.store = store
        self.poll_seconds = poll_seconds
        self.admin_token = admin_token


class Handler(BaseHTTPRequestHandler):
    server: "DaemonHTTPServer"

    def log_message(self, fmt: str, *args: Any) -> None:
        return

    @property
    def state(self) -> DaemonState:
        return self.server.state

    def do_GET(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self.reply({"ok": True})
            return
        if parsed.path.startswith("/api/") and not self.authenticate_admin():
            return
        if parsed.path == "/api/nodes":
            self.reply({"nodes": self.state.store.list_nodes()})
            return
        if parsed.path == "/api/bindings":
            self.reply({"bindings": self.state.store.list_bindings()})
            return
        if parsed.path == "/api/events":
            limit = int(parse_qs(parsed.query).get("limit", ["100"])[0])
            self.reply({"events": self.state.store.list_events(limit)})
            return
        self.reply({"error": "not found"}, HTTPStatus.NOT_FOUND)

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/agent/poll":
            self.agent_poll()
            return
        if parsed.path == "/agent/report":
            self.agent_report()
            return
        if parsed.path.startswith("/api/") and not self.authenticate_admin():
            return
        if parsed.path == "/api/actions/run-natter":
            self.api_run_natter()
            return
        if parsed.path == "/api/bindings":
            self.api_add_binding()
            return
        self.reply({"error": "not found"}, HTTPStatus.NOT_FOUND)

    def read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0:
            return {}
        return json_loads(self.rfile.read(length))

    def reply(self, data: dict[str, Any], status: HTTPStatus = HTTPStatus.OK) -> None:
        raw = json_dumps(data).encode("utf-8")
        self.send_response(int(status))
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def authenticate_agent(self) -> tuple[str, dict[str, Any]] | None:
        node_id = self.headers.get("X-Node-ID", "")
        auth = self.headers.get("Authorization", "")
        if not node_id or not auth.startswith("Bearer "):
            self.reply({"error": "missing node credentials"}, HTTPStatus.UNAUTHORIZED)
            return None
        token = auth.removeprefix("Bearer ").strip()
        row = self.state.store.authenticate_node(node_id, token)
        if row is None:
            self.reply({"error": "invalid node credentials"}, HTTPStatus.FORBIDDEN)
            return None
        return node_id, dict(row)

    def authenticate_admin(self) -> bool:
        if not self.state.admin_token:
            return True
        auth = self.headers.get("Authorization", "")
        if auth == f"Bearer {self.state.admin_token}":
            return True
        self.reply({"error": "invalid admin credentials"}, HTTPStatus.UNAUTHORIZED)
        return False

    def agent_poll(self) -> None:
        auth = self.authenticate_agent()
        if auth is None:
            return
        node_id, _ = auth
        body = self.read_json()
        self.state.store.mark_seen(node_id, body.get("meta", {}))

        command = None
        deadline = time.monotonic() + self.state.poll_seconds
        while time.monotonic() < deadline:
            command = self.state.store.next_command(node_id)
            if command is not None:
                break
            time.sleep(0.5)
        self.reply({"ok": True, "command": command})

    def agent_report(self) -> None:
        auth = self.authenticate_agent()
        if auth is None:
            return
        node_id, _ = auth
        body = self.read_json()
        report_type = body.get("type", "")
        payload = body.get("payload", {})

        if report_type == "action.result":
            self.state.store.complete_command(node_id, str(body.get("command_id", "")), payload)
            self.reply({"ok": True})
            return

        if report_type == "natter.result":
            err = validate_endpoint_payload(payload)
            if err:
                self.reply({"ok": False, "error": err}, HTTPStatus.BAD_REQUEST)
                return
            self.state.store.save_endpoint_lease(node_id, payload)
            bindings = self.state.store.enabled_bindings_for_server(node_id, payload["server_interface"])
            for binding in bindings:
                command = AgentCommand.create(
                    "endpoint.apply",
                    {
                        "binding_id": binding["id"],
                        "interface": binding["client_interface"],
                        "peer_public_key": binding["peer_public_key"],
                        "config_type": binding["config_type"],
                        "config_path": binding["config_path"],
                        "reload_method": binding["reload_method"],
                        "endpoint_host": payload["public_ip"],
                        "endpoint_port": int(payload["public_port"]),
                    },
                )
                self.state.store.queue_command(binding["client_node_id"], command)
            self.reply({"ok": True, "queued": len(bindings)})
            return

        self.state.store.add_event("agent.report", "info", node_id, "", f"Report {report_type}", payload)
        self.reply({"ok": True})

    def api_run_natter(self) -> None:
        body = self.read_json()
        server_node_id = body["server_node_id"]
        server_interface = body["server_interface"]
        command = AgentCommand.create("natter.run", {"server_interface": server_interface})
        self.state.store.queue_command(server_node_id, command)
        self.reply({"ok": True, "command": command.to_json()})

    def api_add_binding(self) -> None:
        body = self.read_json()
        self.state.store.add_binding(body)
        self.reply({"ok": True})


class DaemonHTTPServer(ThreadingHTTPServer):
    def __init__(self, address: tuple[str, int], state: DaemonState):
        super().__init__(address, Handler)
        self.state = state


def validate_endpoint_payload(payload: dict[str, Any]) -> str | None:
    if payload.get("protocol") != "udp":
        return "WireGuard endpoint protocol must be udp"
    if not payload.get("server_interface"):
        return "missing server_interface"
    if not payload.get("public_ip"):
        return "missing public_ip"
    try:
        port = int(payload.get("public_port"))
    except (TypeError, ValueError):
        return "invalid public_port"
    if port < 1 or port > 65535:
        return "invalid public_port"
    return None


def run_server(db_path: str, host: str, port: int, admin_token: str = "") -> None:
    store = Store(db_path)
    store.init()
    server = DaemonHTTPServer((host, port), DaemonState(store, admin_token=admin_token))
    print(f"wgnh daemon listening on http://{host}:{port}")
    server.serve_forever()


def daemon_cli(args: argparse.Namespace) -> None:
    if args.daemon_cmd == "init":
        store = Store(args.db)
        store.init()
        print(f"initialized {args.db}")
        return

    if args.daemon_cmd == "create-node":
        token = generate_token()
        store = Store(args.db)
        store.init()
        store.create_node(args.id, args.name or args.id, args.role, token)
        print(f"node_id={args.id}")
        print(f"token={token}")
        return

    if args.daemon_cmd == "add-binding":
        store = Store(args.db)
        store.init()
        store.add_binding(
            {
                "id": args.id,
                "server_node_id": args.server_node,
                "server_interface": args.server_interface,
                "client_node_id": args.client_node,
                "client_interface": args.client_interface,
                "peer_public_key": args.peer_public_key,
                "config_type": args.config_type,
                "config_path": args.config_path,
                "reload_method": args.reload_method,
            }
        )
        print(f"binding {args.id} saved")
        return

    if args.daemon_cmd == "serve":
        run_server(args.db, args.host, args.port, args.admin_token)
        return

    raise SystemExit("missing daemon subcommand")
