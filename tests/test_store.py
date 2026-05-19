import tempfile
import unittest
from pathlib import Path

from wgnh.auth import generate_token
from wgnh.protocol import AgentCommand
from wgnh.store import Store


class StoreTest(unittest.TestCase):
    def test_store_auth_and_command_queue(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "wgnh.db"
            store = Store(db)
            store.init()
            token = generate_token()
            store.create_node("node-a", "Node A", "server", token)

            self.assertIsNotNone(store.authenticate_node("node-a", token))
            self.assertIsNone(store.authenticate_node("node-a", "bad"))

            command = AgentCommand.create("natter.run", {"server_interface": "wg0"})
            store.queue_command("node-a", command)
            queued = store.next_command("node-a")

            self.assertEqual(queued["command_id"], command.command_id)
            self.assertEqual(queued["action"], "natter.run")


if __name__ == "__main__":
    unittest.main()
