from __future__ import annotations

import contextlib
import importlib.util
import json
import threading
import time
import unittest
import urllib.request
from pathlib import Path
from typing import Any, cast


MODULE_PATH = Path(__file__).with_name("openai_server.py")
SPEC = importlib.util.spec_from_file_location("openai_server", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"unable to load {MODULE_PATH}")
openai_server = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(openai_server)


@contextlib.contextmanager
def run_server():
    server = openai_server.ThreadingHTTPServer(("127.0.0.1", 0), openai_server.Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        deadline = time.time() + 5
        url = f"http://127.0.0.1:{server.server_address[1]}"
        while time.time() < deadline:
            try:
                with urllib.request.urlopen(f"{url}/healthz", timeout=1) as resp:
                    if resp.read().decode("utf-8") == "ok":
                        break
            except Exception:
                time.sleep(0.05)
        else:
            raise AssertionError("server did not become ready")
        yield url
    finally:
        server.shutdown()
        thread.join(timeout=5)
        server.server_close()


class OpenAIServerTests(unittest.TestCase):
    def _post(self, base_url: str, prompt: str) -> dict[str, Any]:
        body = json.dumps(
            {
                "model": "gpt-4o-mini",
                "messages": [
                    {"role": "system", "content": "You are an SRE agent."},
                    {"role": "user", "content": prompt},
                ],
            }
        ).encode("utf-8")
        req = urllib.request.Request(
            f"{base_url}/v1/chat/completions",
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=5) as resp:
            self.assertEqual(resp.status, 200)
            payload: dict[str, Any] = cast(dict[str, Any], json.loads(resp.read().decode("utf-8")))
        self.assertEqual(payload["object"], "chat.completion")
        self.assertIn("choices", payload)
        self.assertTrue(payload["choices"])
        content = cast(str, payload["choices"][0]["message"]["content"])
        return cast(dict[str, Any], json.loads(content))

    def test_strong_signal_returns_local_restart_decision(self) -> None:
        with run_server() as base_url:
            decision = self._post(
                base_url,
                "\n".join(
                    [
                        "App: payments",
                        "Namespace: apps",
                        "Severity: critical",
                        "Status: firing",
                        "Alert: HighErrorRate",
                        "Summary: payments error rate is high",
                    ]
                ),
            )

        self.assertEqual(decision["severity"], "critical")
        self.assertFalse(decision["escalate"])
        self.assertEqual(decision["actions"], [{"type": "rollout_restart", "target": "deployment/payments"}])
        self.assertIn("restart", decision["recommended"])
        self.assertGreater(decision["confidence"], 0.8)

    def test_resolved_alert_is_escalated_conservatively(self) -> None:
        with run_server() as base_url:
            decision = self._post(
                base_url,
                "\n".join(
                    [
                        "App: payments",
                        "Namespace: apps",
                        "Severity: critical",
                        "Status: resolved",
                        "Alert: HighErrorRate",
                        "Summary: payments error rate recovered",
                    ]
                ),
            )

        self.assertTrue(decision["escalate"])
        self.assertEqual(decision["actions"], [])
        self.assertLess(decision["confidence"], 0.5)
        self.assertIn("resolved", decision["diagnosis"])


if __name__ == "__main__":
    unittest.main()
