from __future__ import annotations

import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class ApplyEndpointResult:
    changed: bool
    message: str


def apply_endpoint(
    *,
    config_type: str,
    interface: str,
    peer_public_key: str,
    endpoint_host: str,
    endpoint_port: int,
    config_path: str = "",
    reload_method: str = "none",
    dry_run: bool = False,
) -> ApplyEndpointResult:
    if config_type == "wg_conf":
        result = update_wg_conf(
            Path(config_path or f"/etc/wireguard/{interface}.conf"),
            peer_public_key,
            f"{endpoint_host}:{endpoint_port}",
            dry_run=dry_run,
        )
    elif config_type == "openwrt_uci":
        result = update_openwrt_uci(
            interface,
            peer_public_key,
            endpoint_host,
            endpoint_port,
            dry_run=dry_run,
        )
    elif config_type == "runtime":
        result = wg_set_endpoint(interface, peer_public_key, f"{endpoint_host}:{endpoint_port}", dry_run=dry_run)
    else:
        raise ValueError(f"unsupported config_type: {config_type}")

    if not dry_run and reload_method != "none":
        reload_interface(interface, reload_method)
    return result


def update_wg_conf(path: Path, peer_public_key: str, endpoint: str, dry_run: bool = False) -> ApplyEndpointResult:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    in_peer = False
    matching_peer = False
    endpoint_line_index: int | None = None
    insert_index: int | None = None

    for idx, line in enumerate(lines):
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            if in_peer and matching_peer:
                insert_index = idx
                break
            in_peer = stripped.lower() == "[peer]"
            matching_peer = False
            endpoint_line_index = None
            continue

        if not in_peer or stripped.startswith("#") or "=" not in stripped:
            continue

        key, value = [part.strip() for part in stripped.split("=", 1)]
        if key.lower() == "publickey" and value == peer_public_key:
            matching_peer = True
        elif key.lower() == "endpoint" and matching_peer:
            endpoint_line_index = idx

    if in_peer and matching_peer and insert_index is None:
        insert_index = len(lines)

    if not matching_peer:
        raise ValueError("peer public key not found in wg config")

    new_line = f"Endpoint = {endpoint}\n"
    if endpoint_line_index is not None:
        if lines[endpoint_line_index].strip() == new_line.strip():
            return ApplyEndpointResult(False, "endpoint already up to date")
        lines[endpoint_line_index] = new_line
    else:
        lines.insert(insert_index if insert_index is not None else len(lines), new_line)

    if dry_run:
        return ApplyEndpointResult(True, "dry-run: wg.conf would be updated")

    backup = path.with_suffix(path.suffix + ".bak")
    shutil.copy2(path, backup)
    path.write_text("".join(lines), encoding="utf-8")
    return ApplyEndpointResult(True, f"updated {path}")


def update_openwrt_uci(
    interface: str,
    peer_public_key: str,
    endpoint_host: str,
    endpoint_port: int,
    dry_run: bool = False,
) -> ApplyEndpointResult:
    if dry_run:
        return ApplyEndpointResult(True, f"dry-run: would update OpenWrt {interface} peer endpoint")
    section = find_openwrt_peer_section(interface, peer_public_key)
    run_checked(["uci", "set", f"network.{section}.endpoint_host={endpoint_host}"])
    run_checked(["uci", "set", f"network.{section}.endpoint_port={endpoint_port}"])
    run_checked(["uci", "commit", "network"])
    return ApplyEndpointResult(True, f"updated OpenWrt section {section}")


def find_openwrt_peer_section(interface: str, peer_public_key: str) -> str:
    raw = run_checked(["uci", "show", "network"], capture=True)
    prefix = f"network.@wireguard_{interface}["
    current = ""
    for line in raw.splitlines():
        if line.startswith(prefix) and ".public_key=" in line:
            section = line.split(".public_key=", 1)[0].removeprefix("network.")
            value = line.split("=", 1)[1].strip("'\"")
            if value == peer_public_key:
                current = section
                break
    if not current:
        raise ValueError("peer public key not found in OpenWrt UCI network config")
    return current


def wg_set_endpoint(interface: str, peer_public_key: str, endpoint: str, dry_run: bool = False) -> ApplyEndpointResult:
    if dry_run:
        return ApplyEndpointResult(True, "dry-run: runtime endpoint would be updated")
    run_checked(["wg", "set", interface, "peer", peer_public_key, "endpoint", endpoint])
    return ApplyEndpointResult(True, "updated runtime endpoint")


def reload_interface(interface: str, reload_method: str) -> None:
    if reload_method == "ifup":
        subprocess.run(["ifdown", interface], check=False)
        run_checked(["ifup", interface])
    elif reload_method == "wg-quick-restart":
        run_checked(["systemctl", "restart", f"wg-quick@{interface}"])
    elif reload_method == "network-reload":
        run_checked(["/etc/init.d/network", "reload"])
    else:
        raise ValueError(f"unsupported reload_method: {reload_method}")


def run_checked(args: list[str], capture: bool = False) -> str:
    proc = subprocess.run(
        args,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE,
    )
    return proc.stdout if capture else ""
