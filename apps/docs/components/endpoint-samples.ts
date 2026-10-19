// Request / response samples for the interactive EndpointRail, keyed by
// endpoint. Kept in a .ts module (not inline in MDX) because Prettier's MDX
// formatter dedents multi-line template literals — here the indentation of
// each code sample is preserved exactly as written.

import { DEFAULT_CHAT_MODEL, DEFAULT_CLAUDE_MODEL } from "./models";

export type RailData = {
  baseUrl?: string;
  auth?: string;
  samples: { curl: string; python: string; ts: string };
  /** Copyable request examples when the workbench's stream switch is on. */
  streamSamples?: { curl: string; python: string; ts: string };
  response: string;
  /** Streamed (SSE) response, shown when the `stream` toggle is on. */
  responseStream?: string;
  /** Endpoint-specific error body; falls back to a generic 429 envelope. */
  error?: string;
  streamable?: boolean;
  /** Live-send target for the workbench's "try it" panel. */
  method?: "GET" | "POST";
  path?: string;
  /** Route shown for endpoints that cannot use the JSON live-send panel. */
  displayMethod?: "GET" | "POST";
  displayPath?: string;
  /** Minimal, cheap body used by "try it" — not the documentation sample. */
  tryBody?: string;
};

export const ENDPOINT_SAMPLES: Record<string, RailData> = {
  chat: {
    method: "POST",
    path: "/v1/chat/completions",
    tryBody: `{\n  "model": "${DEFAULT_CHAT_MODEL}",\n  "messages": [{"role": "user", "content": "Say hello from Lazu"}],\n  "max_tokens": 32\n}`,
    streamable: true,
    samples: {
      curl: `curl https://api.lazu.ai/v1/chat/completions \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${DEFAULT_CHAT_MODEL}",
    "messages": [
      {"role": "user", "content": "Say hello from Lazu"}
    ]
  }'`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

resp = client.chat.completions.create(
    model="${DEFAULT_CHAT_MODEL}",
    messages=[{"role": "user", "content": "Say hello"}],
)
print(resp.choices[0].message.content)`,
      ts: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.lazu.ai/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "${DEFAULT_CHAT_MODEL}",
  messages: [{ role: "user", content: "Say hello from Lazu" }],
});`,
    },
    streamSamples: {
      curl: `curl -N https://api.lazu.ai/v1/chat/completions \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${DEFAULT_CHAT_MODEL}",
    "messages": [
      {"role": "user", "content": "Say hello from Lazu"}
    ],
    "stream": true,
    "stream_options": {"include_usage": true}
  }'`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

stream = client.chat.completions.create(
    model="${DEFAULT_CHAT_MODEL}",
    messages=[{"role": "user", "content": "Say hello"}],
    stream=True,
    stream_options={"include_usage": True},
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="")`,
      ts: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.lazu.ai/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const stream = await client.chat.completions.create({
  model: "${DEFAULT_CHAT_MODEL}",
  messages: [{ role: "user", content: "Say hello from Lazu" }],
  stream: true,
  stream_options: { include_usage: true },
});
for await (const chunk of stream) {
  process.stdout.write(chunk.choices[0]?.delta?.content ?? "");
}`,
    },
    response: `{
  "id": "chatcmpl-9aZx",
  "model": "${DEFAULT_CHAT_MODEL}",
  "choices": [
    {
      "index": 0,
      "message": { "role": "assistant", "content": "Hello!" },
      "finish_reason": "stop"
    }
  ],
  "usage": { "prompt_tokens": 11, "completion_tokens": 3 }
}`,
    responseStream: `data: {"id":"chatcmpl-9aZx","choices":[{"delta":{"content":"He"}}]}

data: {"id":"chatcmpl-9aZx","choices":[{"delta":{"content":"llo!"}}]}

data: {"id":"chatcmpl-9aZx","choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-9aZx","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}

data: [DONE]`,
  },

  search: {
    method: "POST",
    path: "/v1/search",
    tryBody: `{\n  "query": "latest OpenAI news",\n  "type": "web",\n  "max_results": 3\n}`,
    samples: {
      curl: `curl https://api.lazu.ai/v1/search \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "query": "latest OpenAI web search API changes",
    "type": "web",
    "max_results": 5,
    "backend": "auto"
  }'`,
      python: `import requests

r = requests.post(
    "https://api.lazu.ai/v1/search",
    headers={"Authorization": "Bearer $LAZU_API_KEY"},
    json={"query": "latest models", "max_results": 5},
)
print(r.json()["results"])`,
      ts: `const r = await fetch("https://api.lazu.ai/v1/search", {
  method: "POST",
  headers: {
    Authorization: "Bearer $LAZU_API_KEY",
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ query: "latest models", max_results: 5 }),
});
const { results } = await r.json();`,
    },
    response: `{
  "query": "latest OpenAI web search API changes",
  "results": [
    {
      "title": "OpenAI ships web search API",
      "url": "https://example.com/news",
      "snippet": "…",
      "score": 0.92,
      "source": "tavily"
    }
  ],
  "usage": {
    "web_search_requests": 1,
    "web_search_billable_units": 1
  },
  "provider_trace": { "backend": "tavily" },
  "route_receipt": {
    "selection": "auto",
    "selected_backend": "search_tavily",
    "chose_provider": "tavily",
    "candidates": ["search_tavily"],
    "decision_reason": "matched web_search capability"
  }
}`,
  },

  models: {
    method: "GET",
    path: "/v1/models",
    baseUrl: "https://api.lazu.ai/v1",
    samples: {
      curl: `curl https://api.lazu.ai/v1/models \\
  -H "Authorization: Bearer $LAZU_API_KEY"`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

for model in client.models.list().data:
    print(model.id, model.owned_by)`,
      ts: `const res = await fetch("https://api.lazu.ai/v1/models", {
  headers: { Authorization: "Bearer $LAZU_API_KEY" },
});
const { data } = await res.json();`,
    },
    response: `{
  "object": "list",
  "data": [
    { "id": "${DEFAULT_CHAT_MODEL}", "object": "model", "owned_by": "openai" },
    { "id": "${DEFAULT_CLAUDE_MODEL}", "object": "model", "owned_by": "anthropic" }
  ]
}`,
  },

  responses: {
    method: "POST",
    path: "/v1/responses",
    tryBody: `{\n  "model": "${DEFAULT_CHAT_MODEL}",\n  "input": "Say hello from Lazu"\n}`,
    samples: {
      curl: `curl https://api.lazu.ai/v1/responses \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${DEFAULT_CHAT_MODEL}",
    "input": [{
      "role": "user",
      "content": [
        {"type": "input_text", "text": "Say hi"}
      ]
    }]
  }'`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

resp = client.responses.create(
    model="${DEFAULT_CHAT_MODEL}",
    input="Say hi",
)
print(resp.output_text)`,
      ts: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.lazu.ai/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.responses.create({
  model: "${DEFAULT_CHAT_MODEL}",
  input: "Say hi",
});`,
    },
    response: `{
  "id": "resp_01ABCDEF",
  "model": "${DEFAULT_CHAT_MODEL}",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [{ "type": "output_text", "text": "Hi!" }]
    }
  ],
  "usage": { "input_tokens": 9, "output_tokens": 2 }
}`,
  },

  files: {
    displayMethod: "POST",
    displayPath: "/v1/files",
    samples: {
      curl: `curl https://api.lazu.ai/v1/files \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -F purpose=user_data \\
  -F file=@./paper.pdf`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

f = client.files.create(
    file=open("paper.pdf", "rb"),
    purpose="user_data",
)
print(f.id)`,
      ts: `import OpenAI from "openai";
import fs from "node:fs";

const client = new OpenAI({
  baseURL: "https://api.lazu.ai/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const f = await client.files.create({
  file: fs.createReadStream("paper.pdf"),
  purpose: "user_data",
});`,
    },
    response: `{
  "id": "file-lazu-01ABCDEF",
  "object": "file",
  "bytes": 421337,
  "filename": "paper.pdf",
  "purpose": "user_data",
  "created_at": 1765980000
}`,
  },

  "usage-requests": {
    displayMethod: "GET",
    displayPath: "/api/usage/requests/{request_id}",
    baseUrl: "https://api.lazu.ai",
    samples: {
      curl: `curl https://api.lazu.ai/api/usage/requests/req_lazu_01ABCDEF \\
  -H "Authorization: Bearer $LAZU_API_KEY"`,
      python: `import requests

r = requests.get(
    "https://api.lazu.ai/api/usage/requests/req_lazu_01ABCDEF",
    headers={"Authorization": "Bearer $LAZU_API_KEY"},
)
print(r.json()["usage"]["dimensions"])`,
      ts: `const r = await fetch(
  "https://api.lazu.ai/api/usage/requests/req_lazu_01ABCDEF",
  { headers: { Authorization: "Bearer $LAZU_API_KEY" } },
);
const detail = await r.json();`,
    },
    response: `{
  "object": "usage.request",
  "id": "req_lazu_01ABCDEF",
  "created_at": 1765980000,
  "model": "${DEFAULT_CHAT_MODEL}",
  "endpoint": "/v1/chat/completions",
  "usage": {
    "prompt_tokens": 1200,
    "completion_tokens": 300,
    "total_tokens": 1500,
    "dimensions": [
      { "name": "input", "quantity": 1200, "unit": "token" },
      { "name": "cache_read", "quantity": 300, "unit": "token" },
      { "name": "output", "quantity": 300, "unit": "token" }
    ]
  },
  "billing": {
    "currency": "USD",
    "amount_microusd": 480,
    "pricing_version": 3,
    "line_items": [
      {
        "dimension": "input",
        "quantity": 1200,
        "unit_microusd": 150,
        "amount_microusd": 180
      }
    ]
  },
  "routing": {
    "status_code": 200,
    "total_duration_ms": 1840,
    "ttft_ms": 420,
    "was_streaming": true
  },
  "provider_usage": {
    "family": "openai",
    "raw_fields": { "prompt_tokens": 1200, "completion_tokens": 300 }
  },
  "log_id": "log_01ABCDEF"
}`,
  },

  embeddings: {
    method: "POST",
    path: "/v1/embeddings",
    tryBody: `{\n  "model": "text-embedding-3-small",\n  "input": ["hello world"]\n}`,
    samples: {
      curl: `curl https://api.lazu.ai/v1/embeddings \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "text-embedding-3-small",
    "input": ["doc one", "doc two"]
  }'`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://api.lazu.ai/v1",
    api_key="YOUR_LAZU_KEY",
)

resp = client.embeddings.create(
    model="text-embedding-3-small",
    input=["doc one", "doc two"],
)
for item in resp.data:
    print(item.embedding[:5])`,
      ts: `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.lazu.ai/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.embeddings.create({
  model: "text-embedding-3-small",
  input: ["doc one", "doc two"],
});`,
    },
    response: `{
  "object": "list",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.0021, -0.013] }
  ],
  "model": "text-embedding-3-small",
  "usage": { "prompt_tokens": 6, "total_tokens": 6 }
}`,
  },
};
