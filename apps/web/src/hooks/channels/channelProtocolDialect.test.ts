import { describe, expect, it } from 'vitest';
import {
  defaultProtocolDialectForChannelType,
  inferProtocolDialect,
  protocolDialectAfterBaseURLChange,
} from './channelProtocolDialect';

describe('channel protocol dialect inference', () => {
  it('uses the effective official OpenAI URL when type 1 has an empty base URL', () => {
    expect(inferProtocolDialect('', 1)).toBe('openai');
    expect(defaultProtocolDialectForChannelType(1)).toBe('openai');
  });

  it('keeps unknown compatible endpoints conservative', () => {
    expect(inferProtocolDialect('https://gateway.example.test/v1', 1)).toBe(
      'openai_chat_compatible',
    );
  });

  it('recognizes supported provider URLs and non-OpenAI channel types', () => {
    expect(
      inferProtocolDialect(
        'https://api.cloudflare.com/client/v4/accounts/example/ai',
        1,
      ),
    ).toBe('cloudflare_ai_v1');
    expect(inferProtocolDialect('https://api.openai.com', 1)).toBe('openai');
    expect(inferProtocolDialect('https://api.deepseek.com', 1)).toBe(
      'deepseek',
    );
    expect(inferProtocolDialect('https://openrouter.ai', 1)).toBe('openrouter');
    expect(inferProtocolDialect('https://tenant.openai.azure.com', 1)).toBe(
      'azure_openai',
    );
    expect(
      inferProtocolDialect('https://tenant.services.ai.azure.com', 1),
    ).toBe('azure_openai');
    expect(inferProtocolDialect('', 14)).toBe('anthropic');
    expect(inferProtocolDialect('', 24)).toBe('gemini');
  });

  it('does not infer a provider from a lookalike hostname or path', () => {
    expect(
      inferProtocolDialect('https://api.openai.com.example.test/v1', 1),
    ).toBe('openai_chat_compatible');
    expect(
      inferProtocolDialect('https://example.test/api.openai.com/', 1),
    ).toBe('openai_chat_compatible');
    expect(
      inferProtocolDialect(
        'https://tenant.services.ai.azure.com.proxy.test',
        1,
      ),
    ).toBe('openai_chat_compatible');
  });

  it('re-infers after the base URL changes unless the operator chose a dialect', () => {
    expect(
      protocolDialectAfterBaseURLChange(
        'https://gateway.example.test/v1',
        1,
        'openai',
        false,
      ),
    ).toBe('openai_chat_compatible');
    expect(
      protocolDialectAfterBaseURLChange(
        'https://gateway.example.test/v1',
        1,
        'openrouter',
        true,
      ),
    ).toBe('openrouter');
  });
});
