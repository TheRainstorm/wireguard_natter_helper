import unittest

from wgnh.agent import parse_natter_stdout


class AgentTest(unittest.TestCase):
    def test_parse_natter_stdout_uses_last_json_line(self) -> None:
        payload = parse_natter_stdout(
            """
noise
{"protocol":"udp","local_ip":"192.168.1.2","local_port":51820,"public_ip":"203.0.113.10","public_port":45182}
"""
        )

        self.assertEqual(
            payload,
            {
                "protocol": "udp",
                "local_ip": "192.168.1.2",
                "local_port": 51820,
                "public_ip": "203.0.113.10",
                "public_port": 45182,
            },
        )


if __name__ == "__main__":
    unittest.main()
