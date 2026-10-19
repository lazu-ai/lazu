#!/usr/bin/env python3
"""Generate an image via configured API profile and save returned base64 payload."""

from __future__ import annotations

import argparse
import base64
import json
import os
import subprocess
import sys
import time
from pathlib import Path

import requests

from config import load_config


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate image with configured API profile")
    parser.add_argument("--profile", "-p", help="Profile name from config.local.yaml")
    parser.add_argument("--prompt", default="一个长裙女子", help="Image prompt")
    parser.add_argument("--model", required=True, help="Image model")
    parser.add_argument("--n", type=int, default=1, help="Number of images")
    parser.add_argument("--quality", help="Image quality, e.g. low/medium/high/auto")
    parser.add_argument("--size", help="Image size, e.g. 1024x1024")
    parser.add_argument("--output-format", default="png", help="Output format, e.g. png/webp/jpeg")
    parser.add_argument("--output-compression", type=int, default=100, help="Output compression level")
    parser.add_argument("--timeout", type=int, default=600, help="Request timeout seconds")
    parser.add_argument(
        "--output-dir",
        default=str(Path(__file__).resolve().parents[2] / ".local-results" / "image-audit"),
        help="Directory to save generated image",
    )
    parser.add_argument("--show-response", action="store_true", help="Print the full JSON response, including base64 image data")
    parser.add_argument("--open", action="store_true", help="Open image after saved")
    return parser.parse_args()


def extract_base64_image(payload_dict: dict) -> str:
    data_list = payload_dict.get("data")
    if isinstance(data_list, list) and data_list:
        first = data_list[0]
        if isinstance(first, dict):
            value = first.get("b64_json") or first.get("base64")
            if isinstance(value, str) and value.strip():
                return value.strip()

    images_list = payload_dict.get("images")
    if isinstance(images_list, list) and images_list:
        first = images_list[0]
        if isinstance(first, dict):
            value = first.get("b64_json") or first.get("base64")
            if isinstance(value, str) and value.strip():
                return value.strip()

    return ""


def normalize_b64_text(b64_text: str) -> str:
    text = b64_text.strip()
    if "," in text and text.lower().startswith("data:image/"):
        text = text.split(",", 1)[1]
    text = text.replace("\n", "").replace("\r", "").replace(" ", "")
    if len(text) % 4:
        text += "=" * (4 - len(text) % 4)
    return text


def save_image(b64_text: str, output_dir: Path, ext: str) -> Path:
    output_dir.mkdir(parents=True, exist_ok=True)
    path = output_dir / f"generated_{int(time.time())}.{ext}"
    path.write_bytes(base64.b64decode(normalize_b64_text(b64_text)))
    return path


def open_image(path: Path) -> None:
    if sys.platform == "darwin":
        subprocess.run(["open", str(path)], check=False)
    elif sys.platform.startswith("win"):
        os.startfile(str(path))  # type: ignore[attr-defined]
    else:
        subprocess.run(["xdg-open", str(path)], check=False)


def main() -> int:
    args = parse_args()
    api_base, api_key, profile = load_config(args.profile)
    url = f"{api_base.rstrip('/')}/images/generations"
    payload = {
        "prompt": args.prompt,
        "model": args.model,
        "n": args.n,
        "output_format": args.output_format,
        "output_compression": args.output_compression,
    }
    if args.quality:
        payload["quality"] = args.quality
    if args.size:
        payload["size"] = args.size
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }

    response = requests.post(url, headers=headers, json=payload, timeout=args.timeout)
    print(f"profile={profile} status={response.status_code}")
    if request_id := response.headers.get("x-lazu-request-id"):
        print(f"request_id={request_id}")
    if cost_usd := response.headers.get("x-lazu-cost-usd"):
        print(f"cost_usd={cost_usd}")

    try:
        body = response.json()
        if args.show_response or not response.ok:
            print(json.dumps(body, ensure_ascii=False, indent=2))
    except json.JSONDecodeError:
        print(response.text)
        return 1

    if not response.ok:
        return 1

    b64_text = extract_base64_image(body)
    if not b64_text:
        print("response does not contain data[0].b64_json/base64 or images[0].b64_json/base64", file=sys.stderr)
        return 1

    saved_path = save_image(b64_text, Path(args.output_dir), args.output_format)
    print(f"saved image: {saved_path}")

    if args.open:
        open_image(saved_path)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
