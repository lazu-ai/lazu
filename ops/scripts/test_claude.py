#!/usr/bin/env python3
"""测试 Claude API: python ops/scripts/test_claude.py [-p profile] [-m model]"""

from anthropic import Anthropic
from config import load_config, parse_args

args = parse_args('测试 Claude API')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'claude-haiku-4-5-20251001'

print(f"\n[{profile}] 测试 Claude API: {api_base}")
print(f"Model: {model}")

client = Anthropic(
    base_url=api_base,
    api_key=api_key
)

response = client.messages.create(
    model=model,
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello"}]
)

print(response.content)