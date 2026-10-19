#!/usr/bin/env python3
"""共享配置加载模块"""

import argparse
import os
import sys

try:
    import yaml
except ImportError:
    print("请安装 PyYAML: pip install pyyaml")
    sys.exit(1)


def load_config(profile=None):
    """加载配置，返回 (api_base, api_key, profile_name)"""
    config_path = os.path.join(os.path.dirname(__file__), 'config.local.yaml')

    if not os.path.exists(config_path):
        print(f"错误: 配置文件不存在: {config_path}")
        print("请复制 config.example.yaml 为 config.local.yaml 并填入配置")
        sys.exit(1)

    with open(config_path, 'r') as f:
        config = yaml.safe_load(f)

    profile_name = profile or config.get('default_profile', 'local')
    profiles = config.get('profiles', {})

    if profile_name not in profiles:
        print(f"错误: Profile '{profile_name}' 不存在")
        print(f"可用: {list(profiles.keys())}")
        sys.exit(1)

    p = profiles[profile_name]
    return p['api_base'], p['api_key'], profile_name


def parse_args(description):
    """通用参数解析"""
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument('-p', '--profile', help='Profile 名称')
    parser.add_argument('-m', '--model', help='模型名称')
    parser.add_argument('-s', '--stream', action='store_true', help='流式输出')
    return parser.parse_args()
