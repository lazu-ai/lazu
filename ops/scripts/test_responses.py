#!/usr/bin/env python3
"""测试 OpenAI Responses API: python ops/scripts/test_responses.py [-p profile] [-m model] [-s]"""

import httpx
from openai import OpenAI
from config import load_config, parse_args

args = parse_args('测试 OpenAI Responses API')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'gpt-4o-mini'

print(f"\n[{profile}] 测试 Responses API: {api_base}")
print(f"Model: {model}, Stream: {args.stream}")

# 初始化客户端
is_local = 'localhost' in api_base or '127.0.0.1' in api_base
http_client = httpx.Client(trust_env=False) if is_local else None
client = OpenAI(
    base_url=api_base,
    api_key=api_key,
    http_client=http_client,
)

# ---- 测试 1: 基础 responses 请求 ----
print("\n--- 测试 1: 基础 responses 请求 ---")
try:
    if args.stream:
        stream = client.responses.create(
            model=model,
            input="Say hello in one word.",
            stream=True,
        )
        for event in stream:
            if event.type == 'response.output_text.delta':
                print(event.delta, end='', flush=True)
            elif event.type == 'response.completed':
                print(f"\n[完成] ID: {event.response.id}")
                if event.response.usage:
                    print(f"  Token 用量: input={event.response.usage.input_tokens}, output={event.response.usage.output_tokens}")
    else:
        response = client.responses.create(
            model=model,
            input="Say hello in one word.",
        )
        print(f"ID: {response.id}")
        print(f"Status: {response.status}")
        for item in response.output:
            if item.type == 'message':
                for content in item.content:
                    if content.type == 'output_text':
                        print(f"Output: {content.text}")
        if response.usage:
            print(f"Token 用量: input={response.usage.input_tokens}, output={response.usage.output_tokens}")
except Exception as e:
    print(f"Error: {e}")

# ---- 测试 2: 带 instructions 的请求 ----
print("\n--- 测试 2: 带 instructions ---")
try:
    response = client.responses.create(
        model=model,
        instructions="You are a math tutor. Be very brief.",
        input="What is 2+2?",
    )
    for item in response.output:
        if item.type == 'message':
            for content in item.content:
                if content.type == 'output_text':
                    print(f"Output: {content.text}")
except Exception as e:
    print(f"Error: {e}")

# ---- 测试 3: 多轮对话 (message-array input) ----
print("\n--- 测试 3: 多轮对话输入 ---")
try:
    response = client.responses.create(
        model=model,
        input=[
            {"role": "user", "content": "My name is Alice."},
            {"role": "assistant", "content": "Hello Alice!"},
            {"role": "user", "content": "What is my name?"},
        ],
    )
    for item in response.output:
        if item.type == 'message':
            for content in item.content:
                if content.type == 'output_text':
                    print(f"Output: {content.text}")
except Exception as e:
    print(f"Error: {e}")

print("\n[完成] 所有测试执行完毕")
