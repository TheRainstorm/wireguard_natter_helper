from __future__ import annotations

import argparse

from .agent import agent_cli
from .daemon import daemon_cli


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="wgnh")
    sub = parser.add_subparsers(dest="cmd", required=True)

    daemon = sub.add_parser("daemon", help="run and manage the VPS daemon")
    daemon_sub = daemon.add_subparsers(dest="daemon_cmd", required=True)

    daemon_init = daemon_sub.add_parser("init", help="initialize the daemon database")
    daemon_init.add_argument("--db", default="wgnh.db")

    create_node = daemon_sub.add_parser("create-node", help="create or rotate a node token")
    create_node.add_argument("--db", default="wgnh.db")
    create_node.add_argument("--id", required=True)
    create_node.add_argument("--name")
    create_node.add_argument("--role", required=True, choices=["server", "client"])

    add_binding = daemon_sub.add_parser("add-binding", help="create or update a peer binding")
    add_binding.add_argument("--db", default="wgnh.db")
    add_binding.add_argument("--id", required=True)
    add_binding.add_argument("--server-node", required=True)
    add_binding.add_argument("--server-interface", required=True)
    add_binding.add_argument("--client-node", required=True)
    add_binding.add_argument("--client-interface", required=True)
    add_binding.add_argument("--peer-public-key", required=True)
    add_binding.add_argument("--config-type", default="openwrt_uci", choices=["openwrt_uci", "wg_conf", "runtime"])
    add_binding.add_argument("--config-path", default="")
    add_binding.add_argument("--reload-method", default="none")

    serve = daemon_sub.add_parser("serve", help="start the daemon HTTP server")
    serve.add_argument("--db", default="wgnh.db")
    serve.add_argument("--host", default="127.0.0.1")
    serve.add_argument("--port", default=8080, type=int)
    serve.add_argument("--admin-token", default="", help="optional bearer token required for /api/*")

    agent = sub.add_parser("agent", help="run a node agent")
    agent.add_argument("--config", required=True)

    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()
    if args.cmd == "daemon":
        daemon_cli(args)
        return
    if args.cmd == "agent":
        agent_cli(args)
        return
    parser.error("missing command")


if __name__ == "__main__":
    main()
