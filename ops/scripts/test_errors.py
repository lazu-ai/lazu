#!/usr/bin/env python3
"""测试错误响应: python ops/scripts/test_errors.py [-p profile]"""

import json
import requests
from config import load_config, parse_args

args = parse_args('测试错误响应')
api_base, api_key, profile = load_config(args.profile)

print(f"\n[{profile}] 测试错误响应: {api_base}")

def test_error(name, url, headers, payload=None):
    print(f"\n{'─' * 40}")
    print(f"测试: {name}")
    try:
        if payload:
            resp = requests.post(url, headers=headers, json=payload)
        else:
            resp = requests.get(url, headers=headers)
        print(f"Status: {resp.status_code}")
        print(json.dumps(resp.json(), indent=2, ensure_ascii=False))
    except Exception as e:
        print(f"Error: {e}")

# 1. 无效 API Key
test_error(
    "无效 API Key",
    f"{api_base}/chat/completions",
    {"Authorization": "Bearer sk-invalid-key", "Content-Type": "application/json"},
    {"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "test"}]}
)

# 2. 无效模型
test_error(
    "无效模型",
    f"{api_base}/chat/completions",
    {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
    {"model": "non-existent-model", "messages": [{"role": "user", "content": "test"}]}
)

# 3. 缺少必填字段 (无 messages)
test_error(
    "缺少 messages",
    f"{api_base}/chat/completions",
    {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
    {"model": "gpt-4o-mini"}
)

# 4. 无效请求格式
test_error(
    "无效请求格式",
    f"{api_base}/chat/completions",
    {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
    {"model": "gpt-4o-mini", "messages": "not an array"}
)

# 5. 无 Authorization 头
test_error(
    "无 Authorization",
    f"{api_base}/chat/completions",
    {"Content-Type": "application/json"},
    {"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "test"}]}
)

# 6. 不存在的端点
test_error(
    "不存在的端点",
    f"{api_base}/nonexistent",
    {"Authorization": f"Bearer {api_key}"}
)

print(f"\n{'=' * 40}")
print("错误测试完成")
