#!/usr/bin/env python3
"""测试最大 max_tokens: python ops/scripts/test_max_tokens.py [-p profile] [-m model]"""

from openai import OpenAI
from config import load_config, parse_args

args = parse_args('测试最大 max_tokens')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'qwen3-max-preview'

print(f"\n[{profile}] 测试最大 max_tokens: {api_base}")
print(f"Model: {model}")

client = OpenAI(
    base_url=api_base,
    api_key=api_key
)

# 使用二分查找来找到最大max_tokens
def test_max_tokens(model_name):
    left, right = 1000, 200000
    max_working = 0

    print(f"开始测试模型 {model_name} 的最大 max_tokens...\n")

    while left <= right:
        mid = (left + right) // 2
        try:
            print(f"测试 max_tokens={mid}...", end=" ")
            response = client.chat.completions.create(
                model=model_name,
                messages=[
                    {"role": "user", "content": "请从1开始数数，一直数下去，每个数字占一行，不要停止，一直数到我让你停止为止"}
                ],
                max_tokens=mid,
            )
            actual_tokens = response.usage.completion_tokens
            print(f"✓ 成功 (实际使用: {actual_tokens} tokens)")
            max_working = mid
            left = mid + 1
        except Exception as e:
            error_msg = str(e)
            print(f"✗ 失败 - {error_msg[:100]}")
            right = mid - 1

    print(f"\n结果: 模型 {model_name} 支持的最大 max_tokens 约为 {max_working}")
    return max_working

if __name__ == "__main__":
    test_max_tokens(model_name=model)
