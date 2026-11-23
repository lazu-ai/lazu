<!-- Lazu · Lazu｜程式設計 Agent、多模態與自動化任務的模型帳戶：Claude、GPT、Gemini 一把 Key · https://lazu.ai/zh-TW/ -->

# 程式設計 Agent、多模態與自動化任務的模型帳戶。

把多平台帳號收進一個開源閘道：原生協定直連前沿模型，同時提供圖像與影片模型。

## 一個帳戶，每個 Agent 一把 Key

- **不用一家家開帳號儲值** — Claude、GPT、Gemini、DeepSeek 都在 lazu 裡。儲值一次，全公司都能用，不用每家單獨註冊、單獨儲值、單獨看帳單。
- **給每個人一份額度，用完就停** — 新同事進來給他 $50，腳本給 $30。花到上限自動停，不會等到月底看帳單才發現有人跑飛了。
- **一人一把 Key，出事只停一把** — 不用全公司共用一把 Key。誰的 Key 傳到 GitHub 上了，停他那把就行，別人照常用。
- **帳單能看懂，也能交給財務** — 誰、用了哪個模型、什麼時候、花了多少，一條條列出來。可以按人或按模型彙總，匯出成表格。

## 三步，十分鐘內跑通

1. **註冊並儲值** — 註冊就有一個團隊帳戶。儲值 $10 起,第一次儲值多送 10%。
2. **設定閘道** — 在控制台選擇要使用的模型和路由。
3. **建立 Key 開始用** — 把 Key 填到工具裡即可呼叫，控制台會顯示用量。

## 直接接入你正在使用的工具

原生協定免改程式碼，公開 /v1/models 自動探索模型；已進入 cc-connect 官方內建預設。

- [cc-connect](https://lazu.ai/docs/zh-TW/integrations/cc-connect) (內建預設) — 在飛書、Telegram、Slack 遠端操作本地 Agent，預設清單直接選擇 Lazu。
- [Claude Code](https://lazu.ai/docs/zh-TW/integrations/claude-code) (原生 Messages) — 保留 Prompt Cache 與思考區塊，預算達上限回傳標準 402 立即停止。
- [Codex CLI](https://lazu.ai/docs/zh-TW/integrations/codex) (原生 Responses) — 支援 wire_api = "responses"，完整保留工具呼叫與推理強度。
- [OpenCode](https://lazu.ai/docs/zh-TW/integrations/opencode) (OpenAI 相容) — 填入 api.lazu.ai/v1，一站切換 Claude、GPT、Gemini 與 DeepSeek。
- [Goose](https://lazu.ai/docs/zh-TW/integrations/goose) (自訂 JSON) — 複製一份自訂 Provider JSON 即可匯入，自動透過 /v1/models 探索模型。
- [更多用戶端與 SDK](https://lazu.ai/docs/zh-TW/integrations/overview) (免金鑰探索) — Cursor、Cline、Cherry Studio、Chatbox 與任意 OpenAI SDK 通用接入。

OpenAI-compatible endpoint: https://api.lazu.ai/v1 · Docs: https://lazu.ai/docs
