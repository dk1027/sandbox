#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

HOST = os.environ.get("HOST", "0.0.0.0")
PORT = int(os.environ.get("PORT", "8081"))
UPSTREAM_BASE_URL = os.environ.get("UPSTREAM_BASE_URL", "").rstrip("/")
UPSTREAM_API_KEY = os.environ.get("UPSTREAM_API_KEY", "")
UPSTREAM_TIMEOUT_SECONDS = int(os.environ.get("UPSTREAM_TIMEOUT_SECONDS", "180"))


class DecisionServer:
    def __init__(self) -> None:
        self.lock = threading.Lock()

    @staticmethod
    def _now_iso() -> str:
        return dt.datetime.now(tz=dt.timezone.utc).isoformat().replace("+00:00", "Z")

    @staticmethod
    def _extract_text(req: dict[str, Any]) -> str:
        parts: list[str] = []
        for message in req.get("messages") or []:
            content = message.get("content")
            if isinstance(content, str):
                parts.append(content)
        return "\n".join(parts)

    @staticmethod
    def _extract_line(text: str, label: str) -> str:
        pattern = rf"(?im)^{re.escape(label)}:\s*(.+)$"
        m = re.search(pattern, text)
        if m:
            return m.group(1).strip()
        return ""

    @staticmethod
    def _extract_app(text: str) -> str:
        m = re.search(r'"app"\s*:\s*"([^"]+)"', text, re.IGNORECASE)
        if m:
            return m.group(1)
        m = re.search(r'\bapp\b\s*[:=]\s*([A-Za-z0-9_.-]+)', text, re.IGNORECASE)
        if m:
            return m.group(1)
        return "payments"

    @staticmethod
    def _extract_status(text: str) -> str:
        status = DecisionServer._extract_line(text, "Status")
        if status:
            return status.lower()
        m = re.search(r'"status"\s*:\s*"([^"]+)"', text, re.IGNORECASE)
        if m:
            return m.group(1).strip().lower()
        return ""

    @staticmethod
    def _extract_severity(text: str) -> str:
        severity = DecisionServer._extract_line(text, "Severity")
        if severity:
            return severity.lower()
        m = re.search(r'"severity"\s*:\s*"([^"]+)"', text, re.IGNORECASE)
        if m:
            return m.group(1).strip().lower()
        return ""

    @staticmethod
    def _proxy(method: str, path: str, body: bytes | None = None) -> tuple[int, bytes, dict[str, str]] | None:
        if not UPSTREAM_BASE_URL:
            return None
        headers = {"Content-Type": "application/json"}
        if UPSTREAM_API_KEY:
            headers["Authorization"] = f"Bearer {UPSTREAM_API_KEY}"
        req = urllib.request.Request(
            f"{UPSTREAM_BASE_URL}{path}",
            data=body,
            headers=headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(req, timeout=UPSTREAM_TIMEOUT_SECONDS) as resp:
                return resp.status, resp.read(), dict(resp.headers.items())
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read(), dict(exc.headers.items()) if exc.headers else {}

    def _build_decision(self, text: str) -> dict[str, Any]:
        app = self._extract_app(text)
        severity = self._extract_severity(text) or "medium"
        status = self._extract_status(text)
        summary = self._extract_line(text, "Summary") or f"{app} alert triaged locally"
        lowered = text.lower()

        resolved_terms = (
            "status: resolved",
            "status resolved",
            "resolved",
            "recovered",
            "healthy",
            "stable",
            "back to normal",
            "no longer firing",
        )
        if status == "resolved" or any(term in lowered for term in resolved_terms):
            return {
                "summary": summary,
                "diagnosis": "alert appears resolved or low-signal",
                "confidence": 0.15,
                "severity": severity,
                "recommended": "monitor and confirm recovery",
                "actions": [],
                "escalate": True,
                "escalation_note": "resolved or low-signal alert",
            }

        strong_terms = (
            "crashloop",
            "crash loop",
            "oomkilled",
            "out of memory",
            "backoff",
            "panic",
            "fatal",
            "segfault",
            "5xx",
            "500",
            "503",
            "timeout",
            "timed out",
            "connection refused",
            "connection reset",
            "error rate",
            "rate is high",
            "unavailable",
            "down",
            "failed",
        )
        moderate_terms = (
            "latency",
            "degraded",
            "slow",
            "warning",
            "increased errors",
            "elevated errors",
        )

        if any(term in lowered for term in strong_terms):
            target = f"deployment/{app}"
            confidence = 0.91 if severity in {"critical", "high"} else 0.84
            action_type = "rollout_restart"
            return {
                "summary": summary,
                "diagnosis": f"detected {severity} service symptoms in the prompt",
                "confidence": confidence,
                "severity": severity,
                "recommended": f"{action_type.replace('_', ' ')} {target}",
                "actions": [{"type": action_type, "target": target}],
                "escalate": False,
            }

        if severity in {"critical", "high"} and any(term in lowered for term in moderate_terms):
            return {
                "summary": summary,
                "diagnosis": "signal suggests degradation, but evidence is too thin for automation",
                "confidence": 0.6,
                "severity": severity,
                "recommended": "collect more evidence and page a human reviewer",
                "actions": [],
                "escalate": True,
                "escalation_note": "insufficient evidence for safe automation",
            }

        return {
            "summary": summary,
            "diagnosis": "alert is ambiguous and should be reviewed manually",
            "confidence": 0.32,
            "severity": severity,
            "recommended": "review the alert and gather more evidence",
            "actions": [],
            "escalate": True,
            "escalation_note": "ambiguous or sparse alert evidence",
        }


SERVER = DecisionServer()


class Handler(BaseHTTPRequestHandler):
    server_version = "sre-llm/0.1"

    def log_message(self, format: str, *args: Any) -> None:
        print(f"[{SERVER._now_iso()}] {self.address_string()} {format % args}", flush=True)

    def _send_json(self, status: int, payload: dict[str, Any]) -> None:
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self) -> None:
        if self.path == "/v1/models" and UPSTREAM_BASE_URL:
            proxied = SERVER._proxy("GET", self.path)
            if proxied is not None:
                status, body, headers = proxied
                self.send_response(status)
                self.send_header("Content-Type", headers.get("Content-Type", "application/json"))
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
        if self.path == "/v1/models":
            self._send_json(
                200,
                {
                    "object": "list",
                    "data": [
                        {
                            "id": "local-heuristic",
                            "object": "model",
                            "created": 0,
                            "owned_by": "local",
                        }
                    ],
                },
            )
            return
        if self.path == "/healthz":
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self._send_json(404, {"error": {"message": "not found"}})

    def do_POST(self) -> None:
        if self.path != "/v1/chat/completions":
            self._send_json(404, {"error": {"message": "not found"}})
            return

        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            req = json.loads(body)
        except json.JSONDecodeError as exc:
            self._send_json(400, {"error": {"message": f"invalid JSON body: {exc}"}})
            return

        if UPSTREAM_BASE_URL:
            proxied = SERVER._proxy("POST", self.path, body=body)
            if proxied is not None:
                status, payload, headers = proxied
                self.send_response(status)
                self.send_header("Content-Type", headers.get("Content-Type", "application/json"))
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return

        decision = SERVER._build_decision(SERVER._extract_text(req))
        response = {
            "id": f"chatcmpl-{os.getpid()}",
            "object": "chat.completion",
            "created": int(__import__("time").time()),
            "model": req.get("model") or "local-heuristic",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": json.dumps(decision, ensure_ascii=False)},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
        }
        self._send_json(200, response)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default=HOST)
    parser.add_argument("--port", type=int, default=PORT)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"Serving heuristic OpenAI-compatible API on http://{args.host}:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
