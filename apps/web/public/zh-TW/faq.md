<!-- Lazu · Lazu 常見問題：Claude / GPT API 付款方式、價格與線路 · https://lazu.ai/zh-TW/faq -->

# Lazu 常見問題：Claude / GPT API 付款方式、價格與線路

## 可以自行部署 Lazu 嗎?

Lazu 目前以 lazu.ai 託管服務的形式提供。如果你需要獨立部署或私有化部署,請聯絡我們,我們會與你討論可行方案。

## 支援多少個模型？

所有可呼叫的模型都列在 lazu.ai/models，附即時價格與最近 7 天的可用率，來自 OpenAI、Anthropic、Google、DeepSeek、月之暗面（Kimi）、智譜（GLM）、阿里（通義千問）和 MiniMax。用 Key 呼叫 GET /api/models/catalog 可以取得這把 Key 能用的同一份列表。

## 支援哪些模型廠商？

OpenAI、Anthropic、Google、DeepSeek、月之暗面（Kimi）、智譜（GLM）、阿里（通義千問）和 MiniMax。新模型上線後會出現在模型目錄與更新日誌中。

## 如何從 OpenAI 遷移過來？

改兩行程式碼：把 base_url 從 https://api.openai.com/v1 改成 https://api.lazu.ai/v1，再把 api_key 換成 Lazu key。業務程式碼可以繼續使用 OpenAI 官方 Python、JavaScript 或 Go SDK。

## 在中國能直接用 Claude 和 GPT 嗎？

可以。使用 Lazu 的介面位址 https://api.lazu.ai，國內 IP 可直連，無需 VPN 或代理。每個模型頁都有最近 7 天的可用率。

## 價格怎麼算？

輸入、輸出與快取 token 分別計量，依你所用線路的公開價格計費。無月費、無最低消費。

## 穩定線路和 Agent 專線有什麼差別？

穩定線路走各家官方 API，按標價計費，可用率最高，適合所有場景。Agent 專線由第三方訂閱池提供：透過官方客戶端接入，不是官方 API。價格低很多，但可用率會有波動，失敗時不會自動切到穩定線路，訂閱池還會附帶自己的預設上下文，所以只建議在對應的 Agent 工具裡使用：Claude 模型搭配 Claude Code，GPT 模型搭配 Codex，Gemini 模型搭配 Antigravity。自己寫的程式或需要精確控制輸出的場景，請用穩定線路。兩條線路用同一把 Key，可以在每把 Key 上按模型選線路。

## 支援什麼付款方式？

透過 Stripe 線上儲值，支援 Visa、Mastercard 等信用卡和支付寶。餘額以美元計價。

## 會用我的資料訓練模型嗎？資料怎麼保存？

不會，Lazu 不拿你的資料訓練模型。請求和回應的內容會記錄在請求日誌中，保留 180 天，方便我們與你一起回查具體請求，到期後刪除。用量紀錄只保存中繼資料（時間、模型、token 數、費用、狀態和 request_id），保留 90 天。

## 支援串流回應嗎？

支援。Chat completion 類 endpoint 支援 SSE 串流輸出，傳 stream: true 即可，行為與 OpenAI 相容。

## 支援 Anthropic 原生 API 嗎？

支援。除了 OpenAI 相容的 /v1/chat/completions，Lazu 還提供 /v1/messages，用 x-api-key 和 anthropic-version header 發起 Anthropic 相容請求。Extended Thinking、Prompt Caching 和 Tool Use 都走這條路徑。

## 速率限制是多少？

速率限制取決於模型與你的帳戶。可在主控台查看即時用量與限制；如需更高的 RPM 或 TPM，請聯絡 support@lazu.ai。

## 如何監控我的使用量和成本？

控制台提供按日、週、月彙總的使用儀表板，並可按模型、endpoint 和 token 類型拆解。每次請求都會返回 x-request-id，可在日誌頁檢索。

## 出錯了怎麼排查？

每個回應都會帶 x-request-id 標頭。在主控台的用量日誌中搜尋這個 ID，可以看到請求時間、使用的線路、狀態、token 計費明細與錯誤詳情。

## 有新用戶優惠嗎？

第一次儲值多送 10%，最多送 $10，和這筆儲值一起入帳。註冊本身不送額度，儲值後不退款。

## 有推薦獎勵嗎？

有。在錢包裡複製你的推薦連結（lazu.ai/?ref=你的名字），也可以在任何 lazu.ai 連結後面加 ?ref=你的名字。好友用它註冊後 30 天內，每筆實付儲值的 10% 歸你，每位好友最多 $30，每筆 7 天後入帳。每人最多算 100 位好友，同一個團隊裡的人不計入。獎勵是 Lazu 額度，不能提領。
