#!/bin/zsh
set -eu
workbench_root="${0:A:h}"
workbench_app="$workbench_root/output/desktop/mac-arm64/Agent Workbench.app"
if [[ ! -d "$workbench_app" ]]; then
  print '尚未打包桌面应用，请查看 docs/development.md 的构建步骤。'
  exit 1
fi
mkdir -p "$workbench_root/data/desktop"
open "$workbench_app" --args "--data-dir=$workbench_root/data/desktop"
