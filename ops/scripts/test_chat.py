#!/usr/bin/env python3
"""测试聊天 API: python ops/scripts/test_chat.py [-p profile] [-m model] [-s]"""

import json
import requests
from config import load_config, parse_args

args = parse_args('测试 Chat API')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'gemini-3-flash-preview'
model = args.model or 'claude-opus-4-8'

print(f"\n[{profile}] 测试 Chat API: {api_base}")

# 测试聊天
url = f"{api_base}/chat/completions"
headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
payload = {
    "model": model,
    "messages": [{"role": "user", "content": "你是什么模型"}],
    "max_tokens": 20,
    "stream": args.stream
}
print(payload['stream'])

print(f"Model: {model}, Stream: {args.stream}")

try:
    if args.stream:
        resp = requests.post(url, headers=headers, json=payload, stream=True)
        print(f"Status: {resp.status_code}")
        for line in resp.iter_lines():
            if line:
                print(line.decode())
    else:
        resp = requests.post(url, headers=headers, json=payload)
        print(f"Status: {resp.status_code}")
        print(json.dumps(resp.json(), indent=4, ensure_ascii=False))
        print(resp.json())
except Exception as e:
    print(f"Error: {e}")
