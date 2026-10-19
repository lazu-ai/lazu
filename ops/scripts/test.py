#!/usr/bin/env python3
"""测试 OpenAI SDK: python ops/scripts/test.py [-p profile] [-m model]"""

import httpx
from openai import OpenAI
from config import load_config, parse_args

args = parse_args('测试 OpenAI SDK')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'gpt-4o-mini'

print(f"\n[{profile}] 测试 OpenAI SDK: {api_base}")
print(f"Model: {model}")

# 初始化客户端（本地测试时绕过系统代理）
is_local = 'localhost' in api_base or '127.0.0.1' in api_base
http_client = httpx.Client(trust_env=False) if is_local else None
client = OpenAI(
    base_url=api_base,
    api_key=api_key,
    http_client=http_client,
)

# 简单对话
response = client.chat.completions.create(
    model=model,
    messages=[
        {"role": "user", "content": "hi"}
    ],
    max_tokens=1000,
)

print(response.choices[0].message.content)
