<!-- Lazu · Lazu FAQ: one API key for Claude, GPT and Gemini: pricing, lanes, privacy · https://lazu.ai/faq -->

# Lazu FAQ: one API key for Claude, GPT and Gemini: pricing, lanes, privacy

## Can I self-host Lazu?

Lazu is offered as a hosted service at lazu.ai. If you need a dedicated or on-premise deployment, contact us and we will walk through the options.

## How many models are supported?

Every model you can call is listed at lazu.ai/models with live prices and the last 7 days of availability: models from OpenAI, Anthropic, Google, DeepSeek, Moonshot (Kimi), Zhipu (GLM), Alibaba (Qwen) and MiniMax. With a key, GET /api/models/catalog returns the same list for that key.

## Which model makers are available?

OpenAI, Anthropic, Google, DeepSeek, Moonshot (Kimi), Zhipu (GLM), Alibaba (Qwen) and MiniMax. New models are added to the model catalog and the changelog as they become available.

## How to migrate from OpenAI?

Change two lines: set base_url from https://api.openai.com/v1 to https://api.lazu.ai/v1, then replace api_key with your Lazu key. Your application code can keep using the official OpenAI Python, JavaScript, or Go SDKs.

## Can I use Claude and GPT directly from China?

Yes. Use the Lazu endpoint at https://api.lazu.ai and connect directly from domestic IP addresses without a VPN or proxy. Each model page shows its availability over the last 7 days.

## How is pricing calculated?

Input, output and cache tokens are counted separately and billed at the published price of the lane you use. There is no monthly fee and no minimum spend.

## What is the difference between the stable and agent lanes?

The stable lane runs on each maker's official API, is billed at the list price, has the highest availability and suits every use. The agent lane runs on third-party subscription pools that serve the model through its official client, not the official API. It costs far less, but availability varies, a failed request does not fall back to the stable lane, and the pool adds its own preset context. So use it only inside the matching agent tool: Claude models in Claude Code, GPT models in Codex, Gemini models in Antigravity. For your own code, or anywhere the output must be controlled exactly, use the stable lane. Both lanes work with the same key, and each key picks the lane per model.

## Which payment methods are supported?

Top up online through Stripe with a card (Visa, Mastercard and others) or Alipay. Balances are in US dollars.

## Does Lazu train on my data? How is it stored?

No. Lazu never trains on your data. Request and response content is recorded in request logs and kept for 180 days, so we can look into a specific request with you, and is then deleted. Usage records keep metadata only (time, model, token counts, cost, status and request_id) for 90 days.

## Are streaming responses supported?

Yes. Chat completion endpoints support SSE streaming with stream: true, matching OpenAI-compatible behavior.

## Is the native Anthropic API supported?

Yes. In addition to OpenAI-compatible /v1/chat/completions, Lazu exposes /v1/messages for Anthropic-compatible requests using x-api-key and anthropic-version headers. Extended Thinking, Prompt Caching, and Tool Use are supported through that path.

## What are the rate limits?

Rate limits depend on the model and your account. You can see live usage and limits in the console; for higher RPM or TPM, contact support@lazu.ai.

## How can I monitor usage and cost?

The console provides usage dashboards by day, week, and month, with breakdowns by model, endpoint, and token type. Every request includes an x-request-id that can be searched in the logs page.

## How should I troubleshoot errors?

Every response includes an x-request-id header. Search that ID in the console's usage log to see when the request ran, which lane served it, its status, the token billing and any error details.

## Is there a new-customer offer?

Your first top-up gets 10% extra, up to $10, credited with the payment. Signing up itself adds no credit, and top-ups aren't refundable.

## Is there a referral reward?

Yes. Copy your referral link from the wallet (lazu.ai/?ref=your-name), or add ?ref=your-name to any lazu.ai link. For 30 days after a friend signs up with it, you get 10% of every top-up they pay, up to $30 per friend, each credited after 7 days. Up to 100 friends count per person, and people in a team with you don't count. Rewards are Lazu credit and can't be withdrawn.
