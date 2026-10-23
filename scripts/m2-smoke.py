#!/usr/bin/env python3
"""Exercise the real M2 process against a local synthetic gateway; never calls a provider."""
import http.server
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import urllib.request
import urllib.error

ROOT = Path(__file__).resolve().parents[1]


def main():
    requests = []

    class Gateway(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            assert self.headers.get("Authorization") == "Bearer M2_SYNTHETIC_PRIVATE_VALUE_DO_NOT_PERSIST"
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            tool_name = body["tools"][0]["function"]["name"]
            assert tool_name in ("get_status", "probe") and len(body["tools"]) == 1
            requests.append(body)
            second = body["messages"][-1]["role"] == "tool"
            if second:
                assert ("read_only_trial" in body["messages"][-1]["content"]) if tool_name == "get_status" else body["messages"][-1]["content"] == "42"
                delta, finish = {"content": "M2 只读试运行通过"}, "stop"
            else:
                delta, finish = {"tool_calls": [{"index": 0, "id": "status-1", "type": "function", "function": {"name": tool_name, "arguments": "{}" if tool_name == "get_status" else "{\"value\":42}"}}]}, "tool_calls"
            frames = [{"choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}, {"choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}]
            raw = ("".join("data: " + json.dumps(frame, ensure_ascii=False) + "\n\n" for frame in frames) + "data: [DONE]\n\n").encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

    gateway = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Gateway)
    thread = threading.Thread(target=gateway.serve_forever, daemon=True)
    thread.start()
    checks = []
    try:
        with tempfile.TemporaryDirectory(prefix="m2-acceptance-", dir=ROOT / "output") as temporary:
            process = None
            base = token = ""

            def start():
                nonlocal process, base, token
                process = subprocess.Popen([str(ROOT / "bin/agent-workbench"), "serve", "--data-dir", temporary], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                ready = json.loads(process.stdout.readline())
                base, token = ready["url"], ready["human_token"]

            def stop():
                nonlocal process
                if process:
                    process.terminate()
                    process.communicate(timeout=8)
                    assert process.returncode == 0, "service did not exit cleanly"
                    process = None

            def call(method, path, body=None):
                raw = None if body is None else json.dumps(body).encode()
                req = urllib.request.Request(base + path, data=raw, method=method, headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
                with urllib.request.urlopen(req, timeout=8) as response:
                    return json.load(response)

            def finish(project, run):
                deadline = time.monotonic() + 8
                while run["status"] == "running" and time.monotonic() < deadline:
                    time.sleep(0.02)
                    run = call("GET", f"/api/projects/{project}/runs/{run['id']}")
                assert run["status"] != "running", "trial did not finish"
                return run

            try:
                start()
                assert call("GET", "/healthz")["stage"] == "M2"
                project = call("POST", "/api/projects", {"name": "M2 验收", "source": "", "idempotency_key": "project"})["id"]
                config = {"endpoint": {"protocol": "chat_completions", "address": f"http://127.0.0.1:{gateway.server_port}/company", "address_kind": "base_url", "allow_local_http": True}, "model": "synthetic", "auth_kind": "bearer", "key_env": "AWB_M2_KEY", "egress": {"mode": "system", "dns_mode": "system"}, "max_output_tokens": 64}
                connection = {"id": "connection", "label": "合成中转", "revision": 1, "account_id": "synthetic-account", "max_concurrent": 1, "enabled": True, "has_key": True, "config": config, "models": [{"id": "synthetic", "tools": "supported", "price_known": True, "currency": "CNY", "input_price_micros_per_million": 1000000, "output_price_micros_per_million": 1000000}]}
                call("PUT", "/api/connections/connection", connection)
                role = call("GET", "/api/role-templates")["templates"][0]
                role.update({"primary": {"connection_id": "connection", "model_id": "synthetic"}, "enabled": True, "tools": ["get_status", "run_command"]})
                role = call("PUT", "/api/roles/" + role["id"], role)
                call("PUT", "/api/budgets/CNY", {"currency": "CNY", "revision": 0, "enabled": True, "limit_micros": 1000000})
                checks.append("真实进程中的连接、角色绑定、价格快照与预算持久化")
                body = {"role_id": role["id"], "role_revision": role["revision"], "input": "合成只读验证", "scope": "conversation_only", "idempotency_key": "trial", "connection_revisions": {"connection": 1}, "values": {"connection": {"AWB_M2_KEY": "M2_SYNTHETIC_PRIVATE_VALUE_DO_NOT_PERSIST"}}}
                run = finish(project, call("POST", f"/api/projects/{project}/runs", body))
                assert run["status"] == "completed" and run["state"]["text"] == "M2 只读试运行通过" and len(requests) == 2
                checks.append("实际 HTTP 流式工具循环与只读能力过滤")
                assert call("POST", f"/api/projects/{project}/runs", body)["id"] == run["id"] and len(requests) == 2
                usage = call("GET", "/api/usage")["summaries"][0]
                assert usage["cost_micros"] == 30 and usage["held_micros"] == 0
                checks.append("运行幂等、用量结算去重及预留释放")
                probe = call("POST", "/api/connections/test", {"config": config, "connection": connection, "values": {"AWB_M2_KEY": "M2_SYNTHETIC_PRIVATE_VALUE_DO_NOT_PERSIST"}, "idempotency_key": "probe"})
                assert probe["tool_verified"] and probe["requests"] == 2 and len(requests) == 4
                assert call("GET", "/api/usage")["summaries"][0]["cost_micros"] == 60
                assert len(call("GET", "/api/projects")) == 1
                checks.append("连接测试同样预约预算和结算费用，不产生可见虚拟项目")
                connection["revision"] = 2
                connection["models"][0]["price_known"] = False
                call("PUT", "/api/connections/connection", connection)
                blocked_body = {**body, "idempotency_key": "unknown-price", "connection_revisions": {"connection": 2}}
                blocked = finish(project, call("POST", f"/api/projects/{project}/runs", blocked_body))
                assert blocked["status"] == "failed" and "价格未知" in blocked["error"] and len(requests) == 4
                checks.append("未知价格在预算预约阶段拒绝，未请求模型")
                stop()
                for file in Path(temporary).glob("workbench.sqlite*"):
                    assert b"M2_SYNTHETIC_PRIVATE_VALUE_DO_NOT_PERSIST" not in file.read_bytes()
                start()
                restored = call("GET", f"/api/projects/{project}/runs/{run['id']}")
                assert restored["status"] == "completed" and len(requests) == 4
                assert len(call("GET", "/api/roles")) == 1
                checks.append("服务重启恢复、无自动重复请求、凭证值未写入 SQLite")
            finally:
                stop()
    finally:
        gateway.shutdown()
        gateway.server_close()
        thread.join(timeout=2)
    report = {"stage": "M2", "status": "passed", "evidence": "real_process_local_synthetic_gateway_no_provider_calls", "checks": checks}
    target = ROOT / "output/verification/m2-acceptance.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(target.read_text())


if __name__ == "__main__":
    main()
