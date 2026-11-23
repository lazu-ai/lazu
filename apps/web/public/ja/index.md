<!-- Lazu · Lazu｜コーディング Agent・動画生成・自動化パイプラインのためのモデルアカウント · https://lazu.ai/ja/ -->

# コーディング Agent・動画生成・自動化パイプラインのためのモデルアカウント。

複数のモデルアカウントを 1 つのオープンソースゲートウェイ に統合。最先端モデルへのネイティブ API と、画像・動画モデルをまとめて提供します。

## アカウントはひとつ、キーはエージェントごとに

- **提供元ごとに契約しなくていい** — Claude、GPT、Gemini、DeepSeek はすべて lazu の中にあります。一度チャージすれば全社で使えて、個別の登録・支払い・請求書は不要です。
- **上限を決めれば、そこで本当に止まる** — 新メンバーには $50、スクリプトには $30。上限に達したらリクエストは止まります。翌月の請求書で気づく、ということはありません。
- **一人一鍵。漏れても被害は一鍵分** — 全社共通のキーは不要です。誰かのキーが公開リポジトリに出ても、その一本を止めれば他の人はそのまま使えます。
- **経理にそのまま渡せる明細** — 誰が・どのモデルで・いつ・いくら使ったかを 1 行ずつ記録します。人単位でもモデル単位でも集計でき、表形式で書き出せます。

## 3 ステップ、10 分で動きます

1. **登録してチャージ** — 登録するとチーム用アカウントができます。チャージは $10 から、初回は 10% 上乗せされます。
2. **ゲートウェイを設定** — コンソールで利用するモデルとルートを選びます。
3. **キーを作成して使い始める** — キーをツールに貼り付ければ使え、コンソールで利用量を確認できます。

## いつものツールにそのまま接続

ネイティブプロトコルと公開 /v1/models 自動取得に対応。cc-connect の公式プリセットにも組み込まれています。

- [cc-connect](https://lazu.ai/docs/ja/integrations/cc-connect) (公式プリセット) — Telegram・Slack・Lark からローカル Agent を操作。プリセットから Lazu を選ぶだけで完了。
- [Claude Code](https://lazu.ai/docs/ja/integrations/claude-code) (ネイティブ Messages) — Prompt Cache と思考ブロックを保持し、予算上限時は HTTP 402 で安全に停止します。
- [Codex CLI](https://lazu.ai/docs/ja/integrations/codex) (ネイティブ Responses) — wire_api = "responses" に対応し、ツール呼び出しと推論強度をそのまま利用可能。
- [OpenCode](https://lazu.ai/docs/ja/integrations/opencode) (OpenAI 互換) — api.lazu.ai/v1 を指定するだけで Claude・GPT・Gemini・DeepSeek を切り替え可能。
- [Goose](https://lazu.ai/docs/ja/integrations/goose) (カスタム JSON) — カスタムプロバイダー JSON を配置するだけで /v1/models からモデルを自動取得。
- [その他のクライアント・SDK](https://lazu.ai/docs/ja/integrations/overview) (公開 /v1/models) — Cursor・Cline・Cherry Studio・Chatbox など OpenAI 互換ツール全般に対応。

OpenAI-compatible endpoint: https://api.lazu.ai/v1 · Docs: https://lazu.ai/docs
