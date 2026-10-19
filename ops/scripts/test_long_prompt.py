#!/usr/bin/env python3
"""发一个 ≥2k tokens 的长 system prompt 请求，用来测 GPT / Gemini / Grok
的计费日志 UI（尤其是 OpenAI 的 prompt cache 路径）。

Usage:
    # 单次请求（看首次是否正确计费）
    python3 ops/scripts/test_long_prompt.py -m gpt-5-mini

    # 跑两次同样的 prompt，第二次应该命中 OpenAI prompt cache
    python3 ops/scripts/test_long_prompt.py -m gpt-5-mini --twice

    # 换 Gemini / Grok（它们的 cache 行为不同，二次请求未必命中）
    python3 ops/scripts/test_long_prompt.py -m gemini-2.5-pro --twice
    python3 ops/scripts/test_long_prompt.py -m grok-4

参考：OpenAI prompt cache 最低 1024 tokens 才会被 cache，第二次相同前缀
      请求会在 usage.prompt_tokens_details.cached_tokens 里看到命中数。
"""

from __future__ import annotations

import json
import sys
import time
import uuid

try:
    import requests
except ImportError:
    print("请先安装依赖：pip install requests", file=sys.stderr)
    sys.exit(1)

from config import load_config, parse_args


# 加个 run salt 避免跟上次缓存撞车（但同一次运行内的两发保持一致）
RUN_SALT = uuid.uuid4().hex
LONG_SYSTEM = (
    f"Session salt [{RUN_SALT}]. You are a careful assistant. "
    "Below is a long context that the user expects you to keep in mind. "
    "Pad content follows to exceed the 1024-token prompt-cache threshold. "
) * 200  # ~2000 tokens，稳稳超过 OpenAI 的 1024 门槛


def send_chat(api_base: str, api_key: str, model: str,
              user_msg: str) -> dict | None:
    url = f"{api_base.rstrip('/')}/chat/completions"
    body = {
        "model": model,
        "max_tokens": 30,
        "messages": [
            {"role": "system", "content": LONG_SYSTEM},
            {"role": "user", "content": user_msg},
        ],
    }
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }
    try:
        resp = requests.post(url, headers=headers, json=body, timeout=60)
    except requests.RequestException as e:
        print(f"  ✗ request failed: {e}", file=sys.stderr)
        return None
    if resp.status_code != 200:
        print(f"  ✗ HTTP {resp.status_code}:\n    {resp.text[:500]}",
              file=sys.stderr)
        return None
    return resp.json()


def dump_usage(label: str, r: dict) -> None:
    usage = r.get("usage", {}) or {}
    prompt = usage.get("prompt_tokens", "?")
    completion = usage.get("completion_tokens", "?")
    total = usage.get("total_tokens", "?")
    # OpenAI / Gemini 都可能把 cache 命中放在 prompt_tokens_details
    details = usage.get("prompt_tokens_details") or {}
    cached = details.get("cached_tokens", 0)
    print(f"  [{label}] prompt={prompt}  completion={completion}  "
          f"total={total}  cached={cached}")


def main() -> int:
    import argparse
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('-p', '--profile', default=None)
    ap.add_argument('-m', '--model', default='gpt-5-mini')
    ap.add_argument('--twice', action='store_true',
                    help='发两次同样的 prompt（测 cache 命中）')
    args = ap.parse_args()

    api_base, api_key, profile = load_config(args.profile)

    print("=" * 60)
    print(f"[{profile}] 长 prompt 测试")
    print(f"  api_base   = {api_base}")
    print(f"  model      = {args.model}")
    print(f"  system len ≈ {len(LONG_SYSTEM)} chars（≈2k tokens）")
    print("=" * 60)

    print(f"\n[1] 发第一次请求")
    r1 = send_chat(api_base, api_key, args.model, "用一句话打个招呼")
    if r1 is None:
        return 1
    dump_usage("req1", r1)

    if args.twice:
        time.sleep(1)
        print(f"\n[2] 发第二次请求（预期触发 prompt cache 命中）")
        r2 = send_chat(api_base, api_key, args.model, "再用一句话打招呼")
        if r2 is None:
            return 1
        dump_usage("req2", r2)
        cached = (r2.get("usage", {}) or {}).get(
            "prompt_tokens_details", {}).get("cached_tokens", 0)
        print(f"\n→ 第二次 cached_tokens={cached}", end='  ')
        print("（OpenAI 类模型应该 > 0）" if cached else
              "（未命中 —— 可能是 Gemini/Grok 没 cache 能力，或上游未启用）")

    print(f"\n✓ 请求完成。刷新「使用日志」页，点开最新 {args.model} "
          "那一行看 LogDetailPanel 计费明细。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
