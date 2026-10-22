#!/usr/bin/env python3
"""Exercise the real M1 local service with temporary source/data; no model calls."""
import http.client
import json
from pathlib import Path
import subprocess
import tempfile
import urllib.request
import urllib.error

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "output"
OUTPUT.mkdir(exist_ok=True)


def main():
    checks = []
    with tempfile.TemporaryDirectory(prefix="m1-acceptance-", dir=OUTPUT) as temporary:
        folder = Path(temporary)
        source = folder / "original"
        source.mkdir()
        (source / "uncommitted.txt").write_text("original uncommitted content")
        (source / ".env").write_text("SYNTHETIC_MUST_BE_EXCLUDED")
        document = folder / "requirements.md"
        document.write_text("# Requirements\nKeep confirmed plans immutable.\n")
        process = None

        def start():
            nonlocal process, base, token
            process = subprocess.Popen([str(ROOT / "bin/agent-workbench"), "serve", "--data-dir", str(folder / "state")], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            ready = json.loads(process.stdout.readline())
            base, token = ready["url"], ready["human_token"]

        def stop():
            nonlocal process
            if process:
                process.terminate()
                try:
                    process.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate()
                    raise AssertionError("M1 backend did not shut down cleanly")
                assert process.returncode == 0, "M1 backend exited unsuccessfully"
                process = None

        def call(method, path, body=None, expected=200, credential=None):
            raw = None if body is None else json.dumps(body).encode()
            request = urllib.request.Request(base + path, data=raw, method=method, headers={"Authorization": "Bearer " + (credential or token), "Content-Type": "application/json"})
            try:
                with urllib.request.urlopen(request, timeout=10) as response:
                    status, value = response.status, json.load(response)
            except urllib.error.HTTPError as error:
                status, value = error.code, json.load(error)
            assert status == expected, (method, path, status, value)
            return value

        def next_event(project, cursor):
            host = base.removeprefix("http://")
            connection = http.client.HTTPConnection(host, timeout=4)
            connection.request("GET", f"/api/projects/{project}/events?cursor={cursor}", headers={"Authorization": "Bearer " + token})
            response = connection.getresponse()
            assert response.status == 200
            try:
                while True:
                    line = response.readline().decode()
                    if line.startswith("data: "):
                        event = json.loads(line[6:])
                        assert event["sequence"] > cursor
                        return event
            finally:
                response.close()
                connection.close()

        base = token = ""
        try:
            start()
            assert call("GET", "/healthz")["status"] == "ok"
            checks.append("真实本地服务健康检查")
            created = call("POST", "/api/projects", {"name": "M1 验收项目", "source": str(source), "idempotency_key": "create-project"})
            project = created["id"]
            snapshot = Path(created["snapshot"])
            assert (snapshot / "uncommitted.txt").read_text() == "original uncommitted content"
            assert not (snapshot / ".env").exists()
            assert any(entry.get("excluded") for entry in created["manifest"])
            (source / "uncommitted.txt").write_text("source changed after import")
            assert (snapshot / "uncommitted.txt").read_text() == "original uncommitted content"
            checks.append("未提交文件副本、排除清单与源项目独立")
            repeated = call("POST", "/api/projects", {"name": "M1 验收项目", "source": str(source), "idempotency_key": "create-project"})
            assert repeated["id"] == project
            checks.append("项目创建幂等重试")
            path = f"/api/projects/{project}"
            call("POST", path + "/documents", {"path": str(document), "idempotency_key": "document"})
            message = {"text": "确认范围：自动任务，不添加额外人工审批。", "idempotency_key": "message"}
            call("POST", path + "/messages", message)
            call("POST", path + "/messages", message)
            call("POST", path + "/messages", {**message, "text": "conflicting retry"}, expected=409)
            state = call("GET", path)
            assert len(state["messages"]) == 1 and state["documents"][0]["status"] == "ready"
            checks.append("资料提取、消息持久化和幂等冲突")
            template = call("GET", "/api/plan-template")
            v1 = call("POST", path + "/plans", {"content": template, "base_version": 0, "reason": "", "idempotency_key": "draft-v1"})
            agent = call("POST", path + "/agent-token", {})["token"]
            confirm = {"expected_hash": v1["hash"], "expected_revision": v1["revision"], "idempotency_key": "confirm-v1"}
            call("POST", path + "/plans/1/confirm", confirm, expected=403, credential=agent)
            call("POST", path + "/plans/1/confirm", {**confirm, "expected_hash": "stale"}, expected=409)
            call("POST", path + "/plans/1/confirm", confirm)
            call("PUT", path + "/plans/1", {**confirm, "content": template}, expected=409, credential=agent)
            checks.append("Agent 无确认权限、精确哈希确认与确认后锁定")
            template["title"] = "M1 V2 变更草案"
            v2 = call("POST", path + "/plans", {"content": template, "base_version": 1, "reason": "讨论中的变更，保留已确认 V1", "idempotency_key": "draft-v2"}, credential=agent)
            state = call("GET", path)
            assert v2["status"] == "draft" and state["project"]["active_version"] == 1
            assert state["plans"][0]["hash"] == v1["hash"]
            checks.append("V2 草案不覆盖 V1、完整版本历史")
            first = next_event(project, 0)
            second = next_event(project, first["sequence"])
            assert second["sequence"] > first["sequence"]
            checks.append("SSE 断线按游标重放")
            old_token = token
            stop()
            start()
            call("GET", "/api/projects", expected=401, credential=old_token)
            state = call("GET", path)
            assert state["project"]["active_version"] == 1 and len(state["plans"]) == 2 and len(state["messages"]) == 1 and len(state["documents"]) == 1
            assert next_event(project, 0)["sequence"] == first["sequence"]
            checks.append("服务重启持久恢复、旧服务凭据失效")
        finally:
            stop()
    report = {"stage": "M1", "evidence": "real_local_service_synthetic_inputs_no_model_calls", "status": "passed", "checks": checks}
    target = OUTPUT / "verification/m1-acceptance.json"
    target.parent.mkdir(exist_ok=True, parents=True)
    target.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
