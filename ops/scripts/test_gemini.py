#!/usr/bin/env python3
"""测试 Gemini API: python ops/scripts/test_gemini.py [-p profile] [-m model]"""

from google import genai
from google.genai import types
from config import load_config, parse_args

args = parse_args('测试 Gemini API')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'gemini-2.5-flash'

# genai SDK 会自动添加路径 (/v1beta/...)，所以需要去掉配置中的 /v1
if api_base.endswith('/v1'):
    api_base = api_base[:-3]

print(f"\n[{profile}] 测试 Gemini API: {api_base}")
print(f"Model: {model}")

# 创建客户端
client = genai.Client(
    api_key=api_key,
    http_options=types.HttpOptions(base_url=api_base)
)

# 发送请求
prompt = "Hello! Please introduce yourself in one sentence."
response = client.models.generate_content(
    model=model,
    contents=prompt
)

# 输出结果
print(response.text)
