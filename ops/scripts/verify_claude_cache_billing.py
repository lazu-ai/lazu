#!/usr/bin/env python3
"""验证 Claude cache 计费修复（PR #124）end-to-end。

发两个完全相同的带 cache_control 的 Claude 请求到目标 lazu 实例，
然后查本地 postgres 的 log.other 字段，自动校验计费是否正确。

Usage:
    # 用 config.local.yaml 的 default_profile
    python3 ops/scripts/verify_claude_cache_billing.py

    # 切换 profile / 模型
    python3 ops/scripts/verify_claude_cache_billing.py -p local -m claude-sonnet-4-6

前置：
    1) ops/scripts/config.local.yaml 里配好目标 profile 的 api_base + api_key
       （例如 local: api_base=http://localhost:3000/v1, api_key=本地创建的 lazu token）
    2) 目标实例已配 claude-sonnet-4-6 的 channel
    3) 本地 docker 里 lazu-postgres 容器在跑

DB 连接默认走 `docker exec lazu-postgres psql -U lazu -d lazu_app`。
可通过 env var 覆盖：DB_CONTAINER / DB_USER / DB_NAME
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import uuid

try:
    import requests
except ImportError:
    print("请先安装依赖：pip install requests", file=sys.stderr)
    sys.exit(1)

from config import load_config, parse_args


# Add a per-run salt so each invocation produces a fresh cache key upstream
# (otherwise the second-and-later runs hit the previous run's cache and the
# verification falsely reports "no 5m creation happened").
RUN_SALT = uuid.uuid4().hex
SYS_TEXT = (
    f"Lazu cache billing verification run [{RUN_SALT}]. "
    "This is filler text to exceed the 1024 token prompt-cache minimum. "
) * 150  # ~1500 tokens, safely over the 1024 threshold


def send_request(api_base: str, api_key: str, model: str, user_msg: str,
                 ttl: str = "") -> dict | None:
    """发一个带 cache_control 的 Claude 请求。
    api_base 来自 config.local.yaml，已经含 /v1，所以拼 /messages。
    ttl='1h' 开启 1 小时 TTL（需上游 channel 放行 extended-cache-ttl beta）。"""
    url = f"{api_base.rstrip('/')}/messages"
    cache_control: dict = {"type": "ephemeral"}
    if ttl == "1h":
        cache_control["ttl"] = "1h"
    body = {
        "model": model,
        "max_tokens": 15,
        "system": [{
            "type": "text",
            "text": SYS_TEXT,
            "cache_control": cache_control,
        }],
        "messages": [{"role": "user", "content": user_msg}],
    }
    headers = {
        "Authorization": f"Bearer {api_key}",
        "anthropic-version": "2023-06-01",
        "Content-Type": "application/json",
    }
    if ttl == "1h":
        headers["anthropic-beta"] = "extended-cache-ttl-2025-04-11"
    try:
        resp = requests.post(url, headers=headers, json=body, timeout=30)
    except requests.RequestException as e:
        print(f"  ✗ request failed: {e}", file=sys.stderr)
        return None
    if resp.status_code != 200:
        print(f"  ✗ HTTP {resp.status_code}:\n    {resp.text[:400]}",
              file=sys.stderr)
        return None
    return resp.json()


def query_logs(db_container: str, db_user: str, db_name: str,
               model: str) -> list[list[str]]:
    sql = f"""
        SELECT
          to_char(to_timestamp(created_at), 'HH24:MI:SS'),
          quota,
          prompt_tokens, completion_tokens,
          coalesce((other::jsonb->>'cache_tokens'),'0'),
          coalesce((other::jsonb->>'cache_creation_tokens'),'0'),
          coalesce((other::jsonb->>'cache_creation_5m_tokens'),'-'),
          coalesce((other::jsonb->>'cache_creation_1h_tokens'),'-'),
          coalesce((other::jsonb->>'cache_ratio'),'-'),
          coalesce((other::jsonb->>'cache_creation_ratio'),'-'),
          coalesce((other::jsonb->>'cache_creation_5m_ratio'),'-'),
          coalesce((other::jsonb->>'cache_creation_1h_ratio'),'-')
        FROM logs
        WHERE model_name='{model}' AND type=2
        ORDER BY created_at DESC LIMIT 2;
    """
    proc = subprocess.run(
        ["docker", "exec", "-i", db_container,
         "psql", "-U", db_user, "-d", db_name, "-At", "-F", "|", "-c", sql],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        print(f"  ✗ psql failed: {proc.stderr[:300]}", file=sys.stderr)
        return []
    return [line.split("|") for line in proc.stdout.strip().splitlines() if line]


def dump_row(label: str, r: dict) -> None:
    print(f"  {label}  time={r['time']}  quota={r['quota']}  "
          f"prompt={r['prompt']}  compl={r['compl']}")
    print(f"      cache_read={r['cache_read']}  cc_total(legacy)={r['cc_total']}  "
          f"cc_5m={r['cc_5m']}  cc_1h={r['cc_1h']}")
    print(f"      ratios:  cache={r['r_cache']}  cc={r['r_cc']}  "
          f"cc_5m={r['r_cc5m']}  cc_1h={r['r_cc1h']}")


def main() -> int:
    args = parse_args('验证 Claude cache 计费修复（PR #124）')
    api_base, api_key, profile = load_config(args.profile)
    model = args.model or 'claude-sonnet-4-6'

    db_container = os.environ.get("DB_CONTAINER", "lazu-postgres")
    db_user = os.environ.get("DB_USER", "lazu")
    db_name = os.environ.get("DB_NAME", "lazu")
    cache_ttl = os.environ.get("CACHE_TTL", "")  # "" = default 5m, "1h" = extended

    tier_label = "1h" if cache_ttl == "1h" else "5m"

    print("=" * 60)
    print(f"[{profile}] 验证 Claude cache 计费修复")
    print(f"  api_base   = {api_base}")
    print(f"  model      = {model}")
    print(f"  cache_ttl  = {tier_label}")
    print(f"  db         = {db_user}@{db_container}/{db_name}")
    print("=" * 60)

    # ---- 1. fire request (cache creation) ----
    print(f"\n[1/4] 发送请求 1（预期触发 {tier_label} cache CREATION）")
    r1 = send_request(api_base, api_key, model, "please say hi", ttl=cache_ttl)
    if r1 is None:
        return 1
    print(f"  upstream usage: {json.dumps(r1.get('usage', {}), ensure_ascii=False)}")

    time.sleep(1)

    # ---- 2. fire request (cache hit) ----
    print(f"\n[2/4] 发送请求 2（预期触发 {tier_label} cache HIT）")
    r2 = send_request(api_base, api_key, model, "please say hello", ttl=cache_ttl)
    if r2 is None:
        return 1
    print(f"  upstream usage: {json.dumps(r2.get('usage', {}), ensure_ascii=False)}")

    # 等 lazu 异步写入 log
    time.sleep(2)

    # ---- 3. 查 DB ----
    print("\n[3/4] 从 postgres 读取 lazu 计费日志")
    rows = query_logs(db_container, db_user, db_name, model)
    if len(rows) < 2:
        print(f"  ✗ 预期 >= 2 条 {model} 的最新日志，实际 {len(rows)} 条",
              file=sys.stderr)
        print("    请确认请求确实到达 lazu 并被计费落库", file=sys.stderr)
        return 1

    cols = ["time", "quota", "prompt", "compl", "cache_read", "cc_total",
            "cc_5m", "cc_1h", "r_cache", "r_cc", "r_cc5m", "r_cc1h"]
    row2 = dict(zip(cols, rows[0]))  # newest = request 2（cache hit）
    row1 = dict(zip(cols, rows[1]))  # older  = request 1（cache creation）

    dump_row("request 1 (creation)", row1)
    dump_row("request 2 (hit)     ", row2)

    # ---- 4. 断言 ----
    print("\n[4/4] 校验")
    passed, failed = 0, 0

    def check(desc: str, ok: bool, detail: str = "") -> None:
        nonlocal passed, failed
        mark = "✓" if ok else "✗"
        suffix = f"  ({detail})" if detail else ""
        print(f"  {mark} {desc}{suffix}")
        if ok:
            passed += 1
        else:
            failed += 1

    cr1, cr2 = row1["r_cache"], row2["r_cache"]
    check("cache_ratio = 0.1（修复 fallback bug）",
          cr1 == "0.1" and cr2 == "0.1",
          f"r1={cr1} r2={cr2}")

    # tier 相关字段只在实际创建该 tier cache 时才写进 log.other
    # 所以 5m 测试只校验 5m 字段，1h 测试只校验 1h 字段
    if cache_ttl == "1h":
        check("cache_creation_1h_ratio = 2.0（1h 层已贯通）",
              row1["r_cc1h"] in ("2", "2.0"),
              f"got {row1['r_cc1h']}")
        cc1_1 = row1["cc_1h"]
        wrote_1h = cc1_1.isdigit() and int(cc1_1) > 0
        check("request 1 写入了 1h cache tokens",
              wrote_1h, f"cc_1h={cc1_1}")
    else:
        check("cache_creation_5m_ratio = 1.25（5m 层已贯通）",
              row1["r_cc5m"] == "1.25",
              f"got {row1['r_cc5m']}")
        cc5_1 = row1["cc_5m"]
        wrote_5m = cc5_1.isdigit() and int(cc5_1) > 0
        check("request 1 写入了 5m cache tokens",
              wrote_5m, f"cc_5m={cc5_1}")

    cread_2 = row2["cache_read"]
    hit = cread_2.isdigit() and int(cread_2) > 0
    check("request 2 命中 cache",
          hit, f"cache_read={cread_2}")

    try:
        q1, q2 = int(row1["quota"]), int(row2["quota"])
        saved_pct = ((q1 - q2) * 100 // q1) if q1 > 0 else 0
        check("request 2 比 request 1 便宜（cache 真省钱）",
              q2 < q1, f"q1={q1} q2={q2} 省了 {saved_pct}%")
    except (ValueError, TypeError):
        check("quota 字段为整数", False,
              f"q1={row1['quota']} q2={row2['quota']}")

    print()
    print("=" * 60)
    if failed == 0:
        print(f"全部 {passed} 项校验通过 ✓")
        return 0
    print(f"{passed} 项通过，{failed} 项失败")
    return 2


if __name__ == "__main__":
    sys.exit(main())
