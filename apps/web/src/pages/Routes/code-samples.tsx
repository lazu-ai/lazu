import type { CatalogEndpoint } from './catalog';
import type { ReactNode } from 'react';

/* Code samples for the model-detail modal: one tab per supported endpoint,
   with cURL / Python / TypeScript variants. Snippets are generated here (not
   taken from the backend example) so every endpoint gets all three languages
   and the base URL matches the environment the console runs on. */

export type SampleLanguage = 'curl' | 'python' | 'typescript';

export const SAMPLE_LANGUAGES: { key: SampleLanguage; label: string }[] = [
  { key: 'curl', label: 'cURL' },
  { key: 'python', label: 'Python' },
  { key: 'typescript', label: 'TypeScript' },
];

const ENDPOINT_LABELS: Record<string, string> = {
  anthropic: 'Messages',
  embeddings: 'Embeddings',
  decisions: 'Decisions',
  gemini: 'Gemini',
  'image-generation': 'Images',
  'jina-rerank': 'Rerank',
  openai: 'Chat Completions',
  'openai-response': 'Responses',
  'video-generation': 'Videos',
};

export const endpointLabel = (type?: string) =>
  ENDPOINT_LABELS[type || ''] || type || 'API';

const methodHasBody = (method: string) => method !== 'GET' && method !== 'HEAD';

const curlJSON = (
  base: string,
  path: string,
  body: string,
  method: string,
  extra = '',
) => `curl -X ${method} ${base}${path} \\
  -H "Authorization: Bearer $LAZU_API_KEY" \\${extra}
  -H "Content-Type: application/json"${
    methodHasBody(method)
      ? ` \\
  -d '${body}'`
      : ''
  }`;

const pyRequests = (
  base: string,
  path: string,
  body: string,
  method: string,
) => `import os
import requests

resp = requests.request(
    "${method}",
    "${base}${path}",
    headers={"Authorization": f"Bearer {os.environ['LAZU_API_KEY']}"},${
      methodHasBody(method)
        ? `
    json=${body},`
        : ''
    }
)
print(resp.json())`;

const tsFetch = (base: string, path: string, body: string, method: string) =>
  `const resp = await fetch("${base}${path}", {
  method: "${method}",
  headers: {
    Authorization: \`Bearer \${process.env.LAZU_API_KEY}\`,
    "Content-Type": "application/json",
  },${
    methodHasBody(method)
      ? `
  body: JSON.stringify(${body}),`
      : ''
  }
});
console.log(await resp.json());`;

export const buildSnippet = (
  language: SampleLanguage,
  endpoint: CatalogEndpoint,
  modelId: string,
  base: string,
): string => {
  const type = endpoint.type || 'openai';
  const path = endpoint.path || '/v1/chat/completions';
  const method = String(endpoint.method || 'POST')
    .trim()
    .toUpperCase();
  const curl = (body: string, extra = '') =>
    curlJSON(base, path, body, method || 'POST', extra);
  const pythonRequest = (body: string) =>
    pyRequests(base, path, body, method || 'POST');
  const typescriptRequest = (body: string) =>
    tsFetch(base, path, body, method || 'POST');

  if (type === 'openai') {
    const body = `{
    "model": "${modelId}",
    "messages": [{ "role": "user", "content": "Hello from Lazu" }]
  }`;
    if (language === 'curl') return curl(body);
    if (method !== 'POST') {
      return language === 'python'
        ? pythonRequest(
            `{"model": "${modelId}", "messages": [{"role": "user", "content": "Hello from Lazu"}]}`,
          )
        : typescriptRequest(
            `{ model: "${modelId}", messages: [{ role: "user", content: "Hello from Lazu" }] }`,
          );
    }
    if (language === 'python') {
      return `import os
from openai import OpenAI

client = OpenAI(
    base_url="${base}/v1",
    api_key=os.getenv("LAZU_API_KEY"),  # API Keys 页创建
)

resp = client.chat.completions.create(
    model="${modelId}",
    messages=[{"role": "user", "content": "Hello from Lazu"}],
)
print(resp.choices[0].message.content)`;
    }
    return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${base}/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "${modelId}",
  messages: [{ role: "user", content: "Hello from Lazu" }],
});
console.log(resp.choices[0].message.content);`;
  }

  if (type === 'openai-response') {
    const body = `{
    "model": "${modelId}",
    "input": "Hello from Lazu"
  }`;
    if (language === 'curl') return curl(body);
    if (method !== 'POST') {
      return language === 'python'
        ? pythonRequest(`{"model": "${modelId}", "input": "Hello from Lazu"}`)
        : typescriptRequest(
            `{ model: "${modelId}", input: "Hello from Lazu" }`,
          );
    }
    if (language === 'python') {
      return `import os
from openai import OpenAI

client = OpenAI(
    base_url="${base}/v1",
    api_key=os.getenv("LAZU_API_KEY"),
)

resp = client.responses.create(model="${modelId}", input="Hello from Lazu")
print(resp.output_text)`;
    }
    return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${base}/v1",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.responses.create({
  model: "${modelId}",
  input: "Hello from Lazu",
});
console.log(resp.output_text);`;
  }

  if (type === 'anthropic') {
    const body = `{
    "model": "${modelId}",
    "max_tokens": 1024,
    "messages": [{ "role": "user", "content": "Hello from Lazu" }]
  }`;
    if (language === 'curl') {
      return curl(body, `\n  -H "anthropic-version: 2023-06-01" \\`);
    }
    if (method !== 'POST') {
      return language === 'python'
        ? pythonRequest(
            `{"model": "${modelId}", "max_tokens": 1024, "messages": [{"role": "user", "content": "Hello from Lazu"}]}`,
          )
        : typescriptRequest(
            `{ model: "${modelId}", max_tokens: 1024, messages: [{ role: "user", content: "Hello from Lazu" }] }`,
          );
    }
    if (language === 'python') {
      return `import os
import anthropic

client = anthropic.Anthropic(
    base_url="${base}",
    api_key=os.getenv("LAZU_API_KEY"),
)

resp = client.messages.create(
    model="${modelId}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello from Lazu"}],
)
print(resp.content[0].text)`;
    }
    return `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  baseURL: "${base}",
  apiKey: process.env.LAZU_API_KEY,
});

const resp = await client.messages.create({
  model: "${modelId}",
  max_tokens: 1024,
  messages: [{ role: "user", content: "Hello from Lazu" }],
});
console.log(resp.content[0]);`;
  }

  if (type === 'decisions') {
    const body = JSON.stringify(
      {
        model: modelId,
        state: 'The customer cannot log in.',
        questions: {
          login_issue: {
            type: 'noul',
            instructions: 'Is this a login problem?',
          },
        },
      },
      null,
      2,
    );
    if (language === 'curl') return curl(body);
    if (language === 'python') return pythonRequest(body);
    return typescriptRequest(body);
  }

  if (type === 'embeddings') {
    const body = `{
    "model": "${modelId}",
    "input": "Hello from Lazu"
  }`;
    if (language === 'curl') return curl(body);
    if (language === 'python') {
      return pythonRequest(
        `{"model": "${modelId}", "input": "Hello from Lazu"}`,
      );
    }
    return typescriptRequest(
      `{ model: "${modelId}", input: "Hello from Lazu" }`,
    );
  }

  if (type === 'image-generation') {
    const prompt = 'A minimalist poster of a sunrise over the ocean';
    const body = `{
    "model": "${modelId}",
    "prompt": "${prompt}",
    "size": "1024x1024"
  }`;
    if (language === 'curl') return curl(body);
    if (language === 'python') {
      return pythonRequest(
        `{"model": "${modelId}", "prompt": "${prompt}", "size": "1024x1024"}`,
      );
    }
    return typescriptRequest(
      `{ model: "${modelId}", prompt: "${prompt}", size: "1024x1024" }`,
    );
  }

  if (type === 'video-generation') {
    const body = `{
    "model": "${modelId}",
    "prompt": "A cinematic product reveal on a clean studio table",
    "seconds": 5,
    "size": "16:9"
  }`;
    if (language === 'curl') return curl(body);
    if (language === 'python') {
      return pythonRequest(
        `{"model": "${modelId}", "prompt": "A cinematic product reveal", "seconds": 5, "size": "16:9"}`,
      );
    }
    return typescriptRequest(
      `{ model: "${modelId}", prompt: "A cinematic product reveal", seconds: 5, size: "16:9" }`,
    );
  }

  if (type === 'jina-rerank') {
    const body = `{
    "model": "${modelId}",
    "query": "Which document is about billing?",
    "documents": ["API billing is usage-based.", "This guide covers images."],
    "top_n": 2
  }`;
    if (language === 'curl') return curl(body);
    if (language === 'python') {
      return pythonRequest(
        `{"model": "${modelId}", "query": "Which document is about billing?", "documents": ["API billing is usage-based.", "This guide covers images."], "top_n": 2}`,
      );
    }
    return typescriptRequest(
      `{ model: "${modelId}", query: "Which document is about billing?", documents: ["API billing is usage-based.", "This guide covers images."], top_n: 2 }`,
    );
  }

  if (type === 'gemini') {
    const body = `{
    "contents": [{ "parts": [{ "text": "Hello from Lazu" }] }]
  }`;
    if (language === 'curl') return curl(body);
    if (language === 'python') {
      return pythonRequest(
        `{"contents": [{"parts": [{"text": "Hello from Lazu"}]}]}`,
      );
    }
    return typescriptRequest(
      `{ contents: [{ parts: [{ text: "Hello from Lazu" }] }] }`,
    );
  }

  // Unknown endpoint type: preserve the catalog-advertised HTTP method.
  const body = `{
    "model": "${modelId}"
  }`;
  if (language === 'curl') return curl(body);
  if (language === 'python') {
    return pythonRequest(`{"model": "${modelId}"}`);
  }
  return typescriptRequest(`{ model: "${modelId}" }`);
};

/* ---- minimal highlighter for the snippets above ---- */

const KEYWORDS: Record<SampleLanguage, RegExp> = {
  curl: /^(curl|export)$/,
  python: /^(import|from|print|def|return|for|in|with|as)$/,
  typescript: /^(import|from|const|await|new|return|console)$/,
};

/** Tokenizes a generated snippet into styled spans. Only needs to handle the
 *  code this module produces — not arbitrary source. */
export const highlightSnippet = (
  code: string,
  language: SampleLanguage,
): ReactNode[] => {
  const nodes: ReactNode[] = [];
  const pattern =
    /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\[\s\S])*'|`(?:[^`\\]|\\.)*`|#[^\n]*|\/\/[^\n]*|-[A-Za-z](?=\s)|\b[A-Za-z_][A-Za-z0-9_]*\b)/g;
  let cursor = 0;
  let key = 0;
  for (const match of code.matchAll(pattern)) {
    const index = match.index ?? 0;
    if (index > cursor) nodes.push(code.slice(cursor, index));
    const token = match[0];
    let className: string | null = null;
    if (/^["'`]/.test(token)) className = 'plaza-code__str';
    else if (token.startsWith('#') || token.startsWith('//')) {
      className = 'plaza-code__cm';
    } else if (/^-[A-Za-z]$/.test(token)) className = 'plaza-code__flag';
    else if (KEYWORDS[language].test(token)) className = 'plaza-code__kw';
    if (className) {
      nodes.push(
        <span key={key++} className={className}>
          {token}
        </span>,
      );
    } else {
      nodes.push(token);
    }
    cursor = index + token.length;
  }
  if (cursor < code.length) nodes.push(code.slice(cursor));
  return nodes;
};
