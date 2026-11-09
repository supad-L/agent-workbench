#!/usr/bin/env bash
# 构建 Agent Workbench 使用的官方 Codex CLI 镜像，并按工作台检测条件自检。
set -euo pipefail
cd "$(dirname "$0")/.."
PODMAN="${PODMAN:-podman}"
NODE_IMAGE="${NODE_IMAGE:-docker.io/library/node:22-bookworm-slim}"
CODEX_VERSION="${CODEX_VERSION:-latest}"
BUILD_TAG="localhost/agent-workbench/codex:build"

echo "== 运行时：$("${PODMAN}" version --format '{{.Client.Version}} / server {{.Server.Version}}')"
echo "== 拉取基础镜像 ${NODE_IMAGE}"
"${PODMAN}" pull "${NODE_IMAGE}"
base_digest="$("${PODMAN}" image inspect --format '{{.Digest}}' "${NODE_IMAGE}")"
echo "   digest: ${base_digest}"

echo "== 构建（@openai/codex@${CODEX_VERSION}）"
"${PODMAN}" build --pull=never \
  --build-arg NODE_IMAGE="${NODE_IMAGE}" --build-arg CODEX_VERSION="${CODEX_VERSION}" \
  --label "io.agent-workbench.base-image=${NODE_IMAGE}" \
  --label "io.agent-workbench.base-digest=${base_digest}" \
  --label "io.agent-workbench.codex.requested=${CODEX_VERSION}" \
  -t "${BUILD_TAG}" -f images/codex/Containerfile images/codex

echo "== 按工作台检测条件自检（断网、只读、UID 1000、无能力、私有 /tmp）"
version="$("${PODMAN}" run --rm --pull=never --no-healthcheck --network=none --read-only --user=1000:1000 \
  --cap-drop=ALL --security-opt=no-new-privileges --memory=256m --cpus=1 --pids-limit=64 \
  --tmpfs /tmp:rw,nosuid,nodev,size=32m,mode=1777 --workdir /tmp \
  --env HOME=/tmp/awb-home --env CODEX_HOME=/tmp/awb-home/codex \
  "${BUILD_TAG}" /usr/local/bin/codex --version | tr -d '\r' | tail -n 1)"
echo "   输出：${version}"
if [[ ! "${version}" =~ ^codex-cli\ [0-9][A-Za-z0-9.+_-]*$ ]]; then
  echo "✗ 版本输出不符合检测格式（应为 codex-cli <版本>）" >&2
  exit 1
fi
cli_version="${version#codex-cli }"
final_tag="localhost/agent-workbench/codex:${cli_version}"
"${PODMAN}" tag "${BUILD_TAG}" "${final_tag}"
# 只移除临时标签；untag 不带名称会删除镜像的全部标签
"${PODMAN}" untag "${final_tag}" "${BUILD_TAG}" >/dev/null 2>&1 || true
image_id="$("${PODMAN}" image inspect --format '{{.Id}}' "${final_tag}")"
[[ "${image_id}" == sha256:* ]] || image_id="sha256:${image_id}"
arch="$("${PODMAN}" image inspect --format '{{.Os}}/{{.Architecture}}' "${final_tag}")"

echo
echo "✓ 镜像已就绪"
echo "  标签：${final_tag}"
echo "  平台：${arch}"
echo "  工具：${version}"
echo "  基础镜像 digest：${base_digest}"
echo "IMAGE_ID=${image_id}"
