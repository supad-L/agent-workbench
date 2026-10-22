#!/usr/bin/env python3
"""Generate the reviewable v1 JSON Schemas and synthetic examples (stdlib only)."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / "contracts"
ID = {"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"}
TEXT = {"type": "string", "minLength": 1, "maxLength": 32768}
HASH = {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"}
POSITIVE = {"type": "integer", "minimum": 1}


def array(item, minimum=0):
    return {"type": "array", "items": item, "minItems": minimum, "maxItems": 256, "uniqueItems": True}


def obj(properties, required=None):
    return {"type": "object", "properties": properties, "required": list(properties) if required is None else required, "additionalProperties": False}


def enum(*values):
    return {"type": "string", "enum": list(values)}


def schema(name, definition):
    return {"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": f"https://agent-workbench.invalid/contracts/{name}.schema.json", **definition}


def generate():
    ROOT.mkdir(exist_ok=True)
    (ROOT / "examples").mkdir(exist_ok=True)
    scope = obj({"read_paths": array(TEXT, 1), "write_dirs": array(TEXT), "patch_only_paths": array(TEXT)})
    basis = obj({"id": ID, "text": TEXT})
    egress = obj({"mode": enum("system", "explicit_proxy", "enforced_proxy"), "dns_mode": enum("system", "proxy"), "proxy_ref": ID}, ["mode", "dns_mode"])
    egress["allOf"] = [
        {"if": {"properties": {"mode": {"const": "system"}}}, "then": {"properties": {"dns_mode": {"const": "system"}}, "not": {"required": ["proxy_ref"]}}},
        {"if": {"properties": {"mode": {"enum": ["explicit_proxy", "enforced_proxy"]}}}, "then": {"properties": {"dns_mode": {"const": "proxy"}}, "required": ["proxy_ref"]}},
    ]
    proxy_endpoint = obj({"schema_version": {"const": 1}, "proxy_id": ID, "label": {"type": "string", "minLength": 1, "maxLength": 256}, "scheme": enum("http", "https", "socks5h"), "host": {"type": "string", "minLength": 1, "maxLength": 253, "pattern": "^[A-Za-z0-9:.][A-Za-z0-9.:-]*$", "anyOf": [{"format": "hostname"}, {"format": "ipv4"}, {"format": "ipv6"}]}, "port": {"type": "integer", "minimum": 1, "maximum": 65535}, "credential_ref": ID, "enabled": {"type": "boolean"}, "revision": POSITIVE}, ["schema_version", "proxy_id", "label", "scheme", "host", "port", "enabled", "revision"])
    network_binding = obj({"schema_version": {"const": 1}, "account_resource_id": ID, "ui_mode": enum("system", "proxy"), "proxy_id": ID, "policy_revision": POSITIVE}, ["schema_version", "account_resource_id", "ui_mode", "policy_revision"])
    network_binding["allOf"] = [
        {"if": {"properties": {"ui_mode": {"const": "system"}}}, "then": {"not": {"required": ["proxy_id"]}}},
        {"if": {"properties": {"ui_mode": {"const": "proxy"}}}, "then": {"required": ["proxy_id"]}},
    ]
    plan = obj({"schema_version": {"const": 1}, "project_id": ID, "version": POSITIVE, "title": TEXT, "requirements": array(basis, 1), "engineering_constraints": array(basis, 1), "non_goals": array(TEXT), "acceptance": array(obj({"id": ID, "requirement_ids": array(ID, 1), "description": TEXT, "verification_profile": ID}), 1), "autonomy": obj({"ordinary_tasks": {"const": "automatic"}, "unresolved_questions": {"const": "ask_user"}, "product_changes": {"const": "user_confirmed"}})})
    task = obj({"schema_version": {"const": 1}, "task_id": ID, "project_id": ID, "plan_version": POSITIVE, "plan_hash": HASH, "contract_revision": POSITIVE, "role_id": ID, "objective": TEXT, "requirement_ids": array(ID, 1), "basis_ids": array(ID, 1), "base_commit": TEXT, "environment_id": ID, "depends_on": array(ID), "scope": scope, "non_goals": array(TEXT), "verification_profile": ID, "verification_hash": HASH, "resource_keys": array(ID), "max_review_rounds": {"type": "integer", "minimum": 1, "maximum": 20}})
    connection = obj({"schema_version": {"const": 1}, "connection_id": ID, "label": TEXT, "execution": enum("direct_api", "codex", "claude"), "protocol": enum("messages", "chat_completions", "responses"), "address": {"type": "string", "format": "uri", "maxLength": 2048}, "address_kind": enum("base_url", "endpoint"), "auth_kind": enum("api_key", "bearer", "cli_login", "oauth", "none"), "credential_ref": ID, "account_resource_id": ID, "model_ids": array(TEXT, 1), "egress": egress, "max_concurrent": {"type": "integer", "minimum": 1, "maximum": 32}}, ["schema_version", "connection_id", "label", "execution", "protocol", "address", "address_kind", "auth_kind", "account_resource_id", "model_ids", "egress", "max_concurrent"])
    finding = obj({"id": ID, "basis_id": ID, "severity": enum("blocking", "nonblocking"), "status": enum("open", "claimed_fixed", "verified_resolved"), "problem": TEXT, "evidence_ref": TEXT, "required_fix": TEXT, "recheck": TEXT})
    review = obj({"schema_version": {"const": 1}, "review_id": ID, "task_id": ID, "attempt_id": ID, "plan_version": POSITIVE, "contract_revision": POSITIVE, "artifact_hash": HASH, "verdict": enum("pass", "needs_revision", "blocked"), "findings": array(finding), "suggestions": array(obj({"id": ID, "text": TEXT, "blocks_delivery": {"const": False}})), "blocker_ref": ID}, ["schema_version", "review_id", "task_id", "attempt_id", "plan_version", "contract_revision", "artifact_hash", "verdict", "findings", "suggestions"])
    review["allOf"] = [
        {"if": {"properties": {"verdict": {"const": "needs_revision"}}}, "then": {"properties": {"findings": {"minItems": 1}}}},
        {"if": {"properties": {"verdict": {"const": "blocked"}}}, "then": {"required": ["blocker_ref"]}},
        {"if": {"properties": {"verdict": {"const": "pass"}}}, "then": {"properties": {"findings": {"items": {"anyOf": [{"properties": {"severity": {"const": "nonblocking"}}}, {"properties": {"status": {"const": "verified_resolved"}}}]}}}}},
    ]
    verification = obj({"schema_version": {"const": 1}, "profile_id": ID, "image_digest": HASH, "runner_digest": HASH, "test_bundle_digest": HASH, "config_digest": HASH, "argv": array(array(TEXT, 1), 1), "acceptance_ids": array(ID, 1), "candidate_scripts_are_authoritative": {"const": False}})
    payloads = {
        "task.dispatch": obj({"contract_hash": HASH}),
        "task.progress": obj({"text": TEXT}),
        "task.submitted": obj({"artifact_id": ID, "artifact_hash": HASH}),
        "task.question": obj({"question_id": ID, "question": TEXT, "basis_ids": array(ID)}),
        "task.answer": obj({"question_id": ID, "decision_id": ID, "text": TEXT}),
        "task.review": obj({"review_id": ID}),
        "task.cancel": obj({"reason": TEXT}),
    }
    envelope = obj({"schema_version": {"const": 1}, "message_id": ID, "project_id": ID, "task_id": ID, "attempt_id": ID, "plan_version": POSITIVE, "contract_revision": POSITIVE, "project_run_epoch": POSITIVE, "task_epoch": POSITIVE, "lease_epoch": POSITIVE, "sender_id": ID, "recipient_id": ID, "kind": enum(*payloads), "idempotency_key": ID, "in_reply_to": ID, "payload": {"type": "object"}}, ["schema_version", "message_id", "project_id", "task_id", "attempt_id", "plan_version", "contract_revision", "project_run_epoch", "task_epoch", "lease_epoch", "sender_id", "recipient_id", "kind", "idempotency_key", "payload"])
    envelope["allOf"] = [{"if": {"properties": {"kind": {"const": kind}}}, "then": {"properties": {"payload": payload}}} for kind, payload in payloads.items()]
    account = obj({"schema_version": {"const": 1}, "resource_id": ID, "realm": TEXT, "identity_source": enum("verified_principal", "credential_fingerprint", "unknown"), "identity_hash": HASH, "refresh_owner_id": ID, "max_concurrent": {"type": "integer", "minimum": 1, "maximum": 32}, "quota_scope": enum("account", "workspace", "service", "unknown")}, ["schema_version", "resource_id", "realm", "identity_source", "max_concurrent", "quota_scope"])
    runtime_event = obj({"schema_version": {"const": 1}, "project_id": ID, "task_id": ID, "attempt_id": ID, "lease_epoch": POSITIVE, "sequence": POSITIVE, "type": enum("text_delta", "tool_started", "tool_finished", "turn_finished", "failure"), "payload": obj({"text": {"type": "string", "maxLength": 32768}, "tool_call_id": ID, "error_code": ID}, [])})
    for name, definition in {"plan": plan, "task": task, "connection": connection, "review": review, "verification": verification, "message": envelope, "account_resource": account, "runtime_event": runtime_event, "proxy_endpoint": proxy_endpoint, "network_binding": network_binding}.items():
        (ROOT / f"{name}.schema.json").write_text(json.dumps(schema(name, definition), ensure_ascii=False, indent=2) + "\n")
    digest = "sha256:" + "a" * 64
    examples = {
        "plan": {"schema_version": 1, "project_id": "PROJECT-1", "version": 1, "title": "自动任务", "requirements": [{"id": "REQ-1", "text": "失败自动重试两次并记录最终结果"}], "engineering_constraints": [{"id": "ENG-1", "text": "满足固定验证，不改变现有兼容行为"}], "non_goals": ["人工审批节点"], "acceptance": [{"id": "AC-1", "requirement_ids": ["REQ-1"], "description": "失败时恰好重试两次", "verification_profile": "verify-1"}], "autonomy": {"ordinary_tasks": "automatic", "unresolved_questions": "ask_user", "product_changes": "user_confirmed"}},
        "task": {"schema_version": 1, "task_id": "TASK-1", "project_id": "PROJECT-1", "plan_version": 1, "plan_hash": digest, "contract_revision": 1, "role_id": "backend", "objective": "实现固定重试策略", "requirement_ids": ["REQ-1"], "basis_ids": ["AC-1", "ENG-1"], "base_commit": "synthetic-base", "environment_id": "fixture-env", "depends_on": [], "scope": {"read_paths": ["."], "write_dirs": ["backend/"], "patch_only_paths": []}, "non_goals": ["人工审批节点"], "verification_profile": "verify-1", "verification_hash": digest, "resource_keys": [], "max_review_rounds": 3},
        "connection": {"schema_version": 1, "connection_id": "company", "label": "公司 Messages 中转（合成示例）", "execution": "direct_api", "protocol": "messages", "address": "https://model.company.invalid/v1/messages", "address_kind": "endpoint", "auth_kind": "bearer", "credential_ref": "secret-reference-only", "account_resource_id": "account-1", "model_ids": ["company-model"], "egress": {"mode": "system", "dns_mode": "system"}, "max_concurrent": 1},
        "review": {"schema_version": 1, "review_id": "REVIEW-1", "task_id": "TASK-1", "attempt_id": "ATTEMPT-1", "plan_version": 1, "contract_revision": 1, "artifact_hash": digest, "verdict": "needs_revision", "findings": [{"id": "F-1", "basis_id": "AC-1", "severity": "blocking", "status": "open", "problem": "仅重试一次", "evidence_ref": "VERIFY-1:retry", "required_fix": "按已确认要求重试两次", "recheck": "retry"}], "suggestions": [{"id": "S-1", "text": "后续可以讨论新的展示方式", "blocks_delivery": False}]},
        "verification": {"schema_version": 1, "profile_id": "verify-1", "image_digest": digest, "runner_digest": digest, "test_bundle_digest": digest, "config_digest": digest, "argv": [["/trusted/runner", "--case", "retry"]], "acceptance_ids": ["AC-1"], "candidate_scripts_are_authoritative": False},
        "message": {"schema_version": 1, "message_id": "MSG-1", "project_id": "PROJECT-1", "task_id": "TASK-1", "attempt_id": "ATTEMPT-1", "plan_version": 1, "contract_revision": 1, "project_run_epoch": 1, "task_epoch": 1, "lease_epoch": 1, "sender_id": "worker-1", "recipient_id": "coordinator-1", "kind": "task.question", "idempotency_key": "question-once-1", "payload": {"question_id": "Q-1", "question": "两份文档对最终失败行为规定不同，应该采用哪一个？", "basis_ids": ["REQ-1"]}},
        "account_resource": {"schema_version": 1, "resource_id": "account-1", "realm": "https://model.company.invalid", "identity_source": "credential_fingerprint", "identity_hash": digest, "max_concurrent": 1, "quota_scope": "unknown"},
        "runtime_event": {"schema_version": 1, "project_id": "PROJECT-1", "task_id": "TASK-1", "attempt_id": "ATTEMPT-1", "lease_epoch": 1, "sequence": 1, "type": "text_delta", "payload": {"text": "正在读取任务上下文"}},
        "proxy_endpoint": {"schema_version": 1, "proxy_id": "proxy-1", "label": "专用出口（合成示例）", "scheme": "socks5h", "host": "proxy.example.invalid", "port": 1080, "credential_ref": "proxy-secret-reference", "enabled": True, "revision": 1},
        "network_binding": {"schema_version": 1, "account_resource_id": "account-1", "ui_mode": "proxy", "proxy_id": "proxy-1", "policy_revision": 1},
    }
    for name, example in examples.items():
        (ROOT / "examples" / f"{name}.json").write_text(json.dumps(example, ensure_ascii=False, indent=2) + "\n")
    operations = [
        ("/api/projects", "post", "human", None),
        ("/api/projects/{id}/messages", "post", "human", None),
        ("/api/projects/{id}/plans/{version}", "get", "human", None),
        ("/api/projects/{id}/plans/{version}/confirm", "post", "human", "confirmation"),
        ("/api/projects/{id}/changes", "post", "human", None),
        ("/api/changes/{id}/confirm", "post", "human", "confirmation"),
        ("/api/clarifications/{id}/answer", "post", "human", None),
        ("/api/projects/{id}/tasks", "get", "human", None),
        ("/api/tasks/{id}/artifacts", "get", "human", None),
        ("/api/projects/{id}/pause", "post", "human", None),
        ("/api/projects/{id}/resume", "post", "human", None),
        ("/api/roles", "get", "human", None),
        ("/api/roles", "post", "human", None),
        ("/api/connections", "get", "human", None),
        ("/api/connections", "post", "human", "connection"),
        ("/api/connections/{id}/auth/start", "post", "human", None),
        ("/api/connections/{id}/auth/complete", "post", "human", None),
        ("/api/connections/{id}/test", "post", "human", None),
        ("/api/proxies", "get", "human", None),
        ("/api/proxies", "post", "human", "proxy_endpoint"),
        ("/api/proxies/{id}", "patch", "human", "proxy_endpoint"),
        ("/api/proxies/{id}", "delete", "human", None),
        ("/api/proxies/{id}/test", "post", "human", None),
        ("/api/account-resources/{id}/network", "put", "human", "network_binding"),
        ("/api/account-resources/{id}/network", "get", "human", None),
        ("/api/projects/{id}/events", "get", "human", None),
        ("/internal/tools/messages", "post", "agent", "message"),
    ]
    paths = {}
    for route, method, principal, body in operations:
        operation = {"operationId": method + "_" + route.strip("/").replace("/", "_").replace("{", "").replace("}", ""), "x-implementation-status": "planned", "security": [{"HumanSession" if principal == "human" else "TaskCapability": []}], "responses": {"200": {"description": "Operation result; does not imply task acceptance"}, "401": {"description": "Authentication required"}, "403": {"description": "Principal lacks this operation"}, "409": {"description": "Stale version, epoch, hash or idempotency conflict"}}}
        parameters = []
        for parameter in ["id", "version"]:
            if "{" + parameter + "}" in route:
                parameters.append({"name": parameter, "in": "path", "required": True, "schema": POSITIVE if parameter == "version" else ID})
        if parameters:
            operation["parameters"] = parameters
        if body:
            ref = "#/components/schemas/Confirmation" if body == "confirmation" else f"./{body}.schema.json"
            operation["requestBody"] = {"required": True, "content": {"application/json": {"schema": {"$ref": ref}}}}
        if route.endswith("/events"):
            operation["parameters"].append({"name": "cursor", "in": "query", "schema": {"type": "integer", "minimum": 0}})
            operation["responses"]["200"]["content"] = {"text/event-stream": {"schema": {"type": "string"}}}
        paths.setdefault(route, {})[method] = operation
    api = {"openapi": "3.1.0", "info": {"title": "agent-workbench interface baseline", "version": "0.1.0", "description": "M0 contract draft; HTTP business services are not implemented yet."}, "paths": paths, "components": {"securitySchemes": {"HumanSession": {"type": "http", "scheme": "bearer", "description": "User-only identity; never supplied to task containers"}, "TaskCapability": {"type": "http", "scheme": "bearer", "description": "Task-scoped identity checked with execution epochs; cannot confirm a plan"}}, "schemas": {"Confirmation": obj({"expected_hash": HASH, "expected_revision": {"type": "integer", "minimum": 0}, "idempotency_key": ID})}}}
    (ROOT / "openapi.json").write_text(json.dumps(api, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    generate()
