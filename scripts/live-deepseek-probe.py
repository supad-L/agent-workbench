#!/usr/bin/env python3
"""Explicit manual DeepSeek connectivity test. Never saves the entered API key."""
import getpass
import os
from pathlib import Path
import subprocess
import sys


def main():
    root = Path(__file__).resolve().parents[1]
    binary = root / "bin/agent-workbench"
    if not binary.is_file():
        raise SystemExit("请先在项目目录执行：python3 scripts/dev.py build")
    if not sys.stdin.isatty() or not sys.stderr.isatty():
        raise SystemExit("请在你自己的交互终端运行，密钥必须使用隐藏输入。")
    print("将向 DeepSeek 官方 API 发出两轮请求，使用 deepseek-flash 非思考模式。")
    print("每轮输出上限 256 tokens；会消耗实际 API 额度。只调用无外部副作用的 probe 工具。")
    print("不会读取项目代码、执行项目命令、保存密钥，或使用其他已有登录账号。")
    print("网络跟随本机路由。取消请按 Ctrl+C。")
    try:
        key = getpass.getpass("粘贴 DeepSeek API Key（输入不显示）：")
    except (KeyboardInterrupt, EOFError):
        raise SystemExit("已取消，没有发出测试请求。") from None
    if not key or len(key) > 4096 or any(ord(char) < 32 or ord(char) == 127 for char in key):
        raise SystemExit("密钥为空或格式无效，没有发出测试请求。")
    env = dict(os.environ)
    env["AWB_DEEPSEEK_API_KEY"] = key
    try:
        result = subprocess.run(
            [str(binary), "api-probe", "--config", str(root / "configs/deepseek-api-probe.example.json")],
            cwd=root,
            env=env,
        )
    finally:
        env.pop("AWB_DEEPSEEK_API_KEY", None)
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
