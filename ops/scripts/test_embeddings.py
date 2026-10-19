#!/usr/bin/env python3
"""测试嵌入 API: python ops/scripts/test_embeddings.py [-p profile] [-m model]"""

import json
import requests
from config import load_config, parse_args

args = parse_args('测试 Embeddings API')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'text-embedding-3-small'

print(f"\n[{profile}] 测试 Embeddings API: {api_base}")
print(f"Model: {model}")

url = f"{api_base}/embeddings"
headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
payload = {"model": model, "input": "Hello world"}

try:
    resp = requests.post(url, headers=headers, json=payload)
    print(f"Status: {resp.status_code}")

    data = resp.json()
    if "data" in data and data["data"]:
        emb = data["data"][0].get("embedding", [])
        print(f"维度: {len(emb)}")
        print(f"前5个值: {emb[:5]}")
        print(f"Usage: {data.get('usage', {})}")
    else:
        print(json.dumps(data, indent=2, ensure_ascii=False))
except Exception as e:
    print(f"Error: {e}")
