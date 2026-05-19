from __future__ import annotations

import json
import secrets
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any


def now_iso() -> str:
    return datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def new_id(prefix: str) -> str:
    return f"{prefix}_{secrets.token_urlsafe(12)}"


def json_dumps(data: Any) -> str:
    return json.dumps(data, ensure_ascii=False, separators=(",", ":"))


def json_loads(raw: bytes | str) -> Any:
    if isinstance(raw, bytes):
        raw = raw.decode("utf-8")
    if not raw:
        return {}
    return json.loads(raw)


@dataclass(frozen=True)
class AgentCommand:
    command_id: str
    action: str
    payload: dict[str, Any]
    deadline: str | None = None
    idempotency_key: str | None = None

    @classmethod
    def create(cls, action: str, payload: dict[str, Any]) -> "AgentCommand":
        command_id = new_id("cmd")
        return cls(
            command_id=command_id,
            action=action,
            payload=payload,
            deadline=None,
            idempotency_key=command_id,
        )

    def to_json(self) -> dict[str, Any]:
        return {
            "command_id": self.command_id,
            "action": self.action,
            "payload": self.payload,
            "deadline": self.deadline,
            "idempotency_key": self.idempotency_key,
        }
