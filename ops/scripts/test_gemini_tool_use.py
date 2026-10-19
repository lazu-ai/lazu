#!/usr/bin/env python3
"""测试 Gemini tool use (genai SDK): python ops/scripts/test_gemini_tool_use.py [-p profile] [-m model]"""

from google import genai
from google.genai import types

from config import load_config, parse_args

args = parse_args('测试 Gemini tool use (genai SDK)')
api_base, api_key, profile = load_config(args.profile)
model = args.model or 'gemini-2.5-flash'

# genai SDK 会自动添加路径 (/v1beta/...)，所以需要去掉配置中的 /v1
if api_base.endswith('/v1'):
    api_base = api_base[:-3]

print(f"\n[{profile}] 测试 Gemini tool use: {api_base}")
print(f"Model: {model}")


def get_current_temperature(location: str) -> dict:
    """Gets the current temperature for a given location."""
    return {"location": location, "temperature": 26, "unit": "C"}


client = genai.Client(
    api_key=api_key,
    http_options=types.HttpOptions(base_url=api_base),
)

config = types.GenerateContentConfig(
    tools=[get_current_temperature],
    tool_config=types.ToolConfig(
        function_calling_config=types.FunctionCallingConfig(mode='ANY'),
    ),
)

prompt = "What's the temperature in Shanghai? Please use the tool."
response = client.models.generate_content(
    model=model,
    contents=prompt,
    config=config,
)

print(response.text)
