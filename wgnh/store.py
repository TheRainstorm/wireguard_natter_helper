from __future__ import annotations

import sqlite3
from pathlib import Path
from typing import Any

from .auth import hash_token, verify_token
from .protocol import AgentCommand, json_dumps, json_loads, now_iso


SCHEMA = """
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('server', 'client')),
  token_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'offline',
  platform TEXT NOT NULL DEFAULT '',
  agent_version TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS bindings (
  id TEXT PRIMARY KEY,
  server_node_id TEXT NOT NULL,
  server_interface TEXT NOT NULL,
  client_node_id TEXT NOT NULL,
  client_interface TEXT NOT NULL,
  peer_public_key TEXT NOT NULL,
  config_type TEXT NOT NULL DEFAULT 'openwrt_uci',
  config_path TEXT NOT NULL DEFAULT '',
  reload_method TEXT NOT NULL DEFAULT 'none',
  endpoint_host TEXT NOT NULL DEFAULT '',
  endpoint_port INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS endpoint_leases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_node_id TEXT NOT NULL,
  server_interface TEXT NOT NULL,
  protocol TEXT NOT NULL,
  local_ip TEXT NOT NULL,
  local_port INTEGER NOT NULL,
  public_ip TEXT NOT NULL,
  public_port INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS commands (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  action TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  result_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  delivered_at TEXT,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  severity TEXT NOT NULL,
  node_id TEXT NOT NULL DEFAULT '',
  binding_id TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
"""


class Store:
    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.conn = sqlite3.connect(self.path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row

    def init(self) -> None:
        self.conn.executescript(SCHEMA)
        self.conn.commit()

    def create_node(self, node_id: str, name: str, role: str, token: str) -> None:
        with self.conn:
            self.conn.execute(
                """
                INSERT INTO nodes (id, name, role, token_hash)
                VALUES (?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                  name=excluded.name,
                  role=excluded.role,
                  token_hash=excluded.token_hash
                """,
                (node_id, name, role, hash_token(token)),
            )
            self.add_event("node.upserted", "info", node_id, "", f"Node {node_id} saved", {})

    def authenticate_node(self, node_id: str, token: str) -> sqlite3.Row | None:
        row = self.conn.execute("SELECT * FROM nodes WHERE id = ?", (node_id,)).fetchone()
        if row is None:
            return None
        if not verify_token(token, row["token_hash"]):
            return None
        return row

    def mark_seen(self, node_id: str, meta: dict[str, Any]) -> None:
        with self.conn:
            self.conn.execute(
                """
                UPDATE nodes
                SET status='online', last_seen_at=?, platform=?, agent_version=?
                WHERE id=?
                """,
                (
                    now_iso(),
                    str(meta.get("platform", "")),
                    str(meta.get("agent_version", "")),
                    node_id,
                ),
            )

    def add_binding(self, data: dict[str, Any]) -> None:
        with self.conn:
            self.conn.execute(
                """
                INSERT INTO bindings (
                  id, server_node_id, server_interface, client_node_id,
                  client_interface, peer_public_key, config_type, config_path,
                  reload_method, enabled
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                  server_node_id=excluded.server_node_id,
                  server_interface=excluded.server_interface,
                  client_node_id=excluded.client_node_id,
                  client_interface=excluded.client_interface,
                  peer_public_key=excluded.peer_public_key,
                  config_type=excluded.config_type,
                  config_path=excluded.config_path,
                  reload_method=excluded.reload_method,
                  enabled=excluded.enabled
                """,
                (
                    data["id"],
                    data["server_node_id"],
                    data["server_interface"],
                    data["client_node_id"],
                    data["client_interface"],
                    data["peer_public_key"],
                    data.get("config_type", "openwrt_uci"),
                    data.get("config_path", ""),
                    data.get("reload_method", "none"),
                    1 if data.get("enabled", True) else 0,
                ),
            )
            self.add_event("binding.upserted", "info", "", data["id"], f"Binding {data['id']} saved", data)

    def queue_command(self, node_id: str, command: AgentCommand) -> None:
        with self.conn:
            self.conn.execute(
                """
                INSERT INTO commands (id, node_id, action, payload_json, created_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (command.command_id, node_id, command.action, json_dumps(command.payload), now_iso()),
            )
            self.add_event("command.queued", "info", node_id, "", f"Queued {command.action}", command.to_json())

    def next_command(self, node_id: str) -> dict[str, Any] | None:
        with self.conn:
            row = self.conn.execute(
                """
                SELECT * FROM commands
                WHERE node_id=? AND status='pending'
                ORDER BY created_at ASC
                LIMIT 1
                """,
                (node_id,),
            ).fetchone()
            if row is None:
                return None
            self.conn.execute(
                "UPDATE commands SET status='delivered', delivered_at=? WHERE id=?",
                (now_iso(), row["id"]),
            )
        return {
            "command_id": row["id"],
            "action": row["action"],
            "payload": json_loads(row["payload_json"]),
        }

    def complete_command(self, node_id: str, command_id: str, result: dict[str, Any]) -> None:
        with self.conn:
            self.conn.execute(
                """
                UPDATE commands
                SET status='completed', result_json=?, completed_at=?
                WHERE id=? AND node_id=?
                """,
                (json_dumps(result), now_iso(), command_id, node_id),
            )
            ok = bool(result.get("ok"))
            self.add_event(
                "command.completed" if ok else "command.failed",
                "info" if ok else "error",
                node_id,
                str(result.get("binding_id", "")),
                f"Command {command_id} {'completed' if ok else 'failed'}",
                result,
            )

    def save_endpoint_lease(self, node_id: str, payload: dict[str, Any]) -> None:
        with self.conn:
            self.conn.execute(
                """
                INSERT INTO endpoint_leases (
                  server_node_id, server_interface, protocol, local_ip,
                  local_port, public_ip, public_port, created_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    node_id,
                    payload["server_interface"],
                    payload["protocol"],
                    payload.get("local_ip", ""),
                    int(payload.get("local_port", 0)),
                    payload["public_ip"],
                    int(payload["public_port"]),
                    now_iso(),
                ),
            )
            self.conn.execute(
                """
                UPDATE bindings
                SET endpoint_host=?, endpoint_port=?
                WHERE server_node_id=? AND server_interface=? AND enabled=1
                """,
                (payload["public_ip"], int(payload["public_port"]), node_id, payload["server_interface"]),
            )
            self.add_event("endpoint.updated", "info", node_id, "", "Endpoint lease saved", payload)

    def enabled_bindings_for_server(self, node_id: str, interface: str) -> list[sqlite3.Row]:
        return list(
            self.conn.execute(
                """
                SELECT * FROM bindings
                WHERE server_node_id=? AND server_interface=? AND enabled=1
                ORDER BY id
                """,
                (node_id, interface),
            )
        )

    def list_nodes(self) -> list[dict[str, Any]]:
        return [dict(row) for row in self.conn.execute("SELECT id,name,role,status,platform,agent_version,last_seen_at FROM nodes ORDER BY id")]

    def list_bindings(self) -> list[dict[str, Any]]:
        return [dict(row) for row in self.conn.execute("SELECT * FROM bindings ORDER BY id")]

    def list_events(self, limit: int = 100) -> list[dict[str, Any]]:
        return [
            dict(row)
            for row in self.conn.execute(
                "SELECT * FROM events ORDER BY id DESC LIMIT ?",
                (limit,),
            )
        ]

    def add_event(
        self,
        event_type: str,
        severity: str,
        node_id: str,
        binding_id: str,
        message: str,
        payload: dict[str, Any],
    ) -> None:
        self.conn.execute(
            """
            INSERT INTO events (type, severity, node_id, binding_id, message, payload_json, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (event_type, severity, node_id, binding_id, message, json_dumps(payload), now_iso()),
        )
