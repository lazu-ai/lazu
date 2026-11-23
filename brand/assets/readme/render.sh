#!/usr/bin/env bash
# Render every README figure from readme-figures.html with headless Chrome at
# 2x. Needs network for Geist and IBM Plex Mono. Writes
# <fig>-<lang>-<theme>.png next to this script.
set -euo pipefail
cd "$(dirname "$0")"
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
for fig in flow ledger session; do
  case $fig in flow) h=600 ;; ledger) h=612 ;; session) h=466 ;; esac
  for lang in en zh; do
    for theme in light dark; do
      out="$fig-$lang-$theme.png"
      "$chrome" --headless=new --hide-scrollbars --force-device-scale-factor=2 \
        --allow-file-access-from-files --default-background-color=00000000 \
        --window-size=960,"$h" --virtual-time-budget=6000 \
        --screenshot="$out" \
        "file://$PWD/readme-figures.html?fig=$fig&lang=$lang&theme=$theme" \
        2>/dev/null
      echo "$out"
    done
  done
done
# Lossless size pass when oxipng is installed.
if command -v oxipng >/dev/null; then oxipng -o 4 --strip safe -q ./*.png; fi
