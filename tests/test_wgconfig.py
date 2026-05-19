import tempfile
import unittest
from pathlib import Path

from wgnh.wgconfig import update_wg_conf


class WgConfigTest(unittest.TestCase):
    def test_update_wg_conf_replaces_matching_peer_endpoint(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "wg0.conf"
            config.write_text(
                """[Interface]
Address = 10.0.0.2/32

[Peer]
PublicKey = server-key
AllowedIPs = 10.0.0.1/32
Endpoint = 1.1.1.1:1111

[Peer]
PublicKey = other-key
Endpoint = 2.2.2.2:2222
""",
                encoding="utf-8",
            )

            result = update_wg_conf(config, "server-key", "203.0.113.10:45182")

            self.assertTrue(result.changed)
            text = config.read_text(encoding="utf-8")
            self.assertIn("Endpoint = 203.0.113.10:45182", text)
            self.assertIn("Endpoint = 2.2.2.2:2222", text)
            self.assertTrue(config.with_suffix(".conf.bak").exists())

    def test_update_wg_conf_inserts_missing_endpoint(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "wg0.conf"
            config.write_text(
                """[Interface]
Address = 10.0.0.2/32

[Peer]
PublicKey = server-key
AllowedIPs = 10.0.0.1/32
""",
                encoding="utf-8",
            )

            result = update_wg_conf(config, "server-key", "203.0.113.10:45182")

            self.assertTrue(result.changed)
            self.assertIn("Endpoint = 203.0.113.10:45182", config.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
