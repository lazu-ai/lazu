import { describe, expect, it } from 'vitest';
import { buildSnippet } from './code-samples';

describe('buildSnippet', () => {
  it.each(['python', 'typescript'] as const)(
    'does not let a placeholder Gemini query key override %s auth',
    (language) => {
      const snippet = buildSnippet(
        language,
        {
          method: 'POST',
          path: '/v1beta/models/gemini-2:generateContent',
          type: 'gemini',
        },
        'gemini-2',
        'https://api.example.com',
      );

      expect(snippet).not.toContain('key=KEY');
      expect(snippet).toContain('LAZU_API_KEY');
    },
  );

  it.each(['curl', 'python', 'typescript'] as const)(
    'honors a catalog-advertised PATCH method in %s',
    (language) => {
      const snippet = buildSnippet(
        language,
        { method: 'patch', path: '/v1/custom', type: 'openai' },
        'custom-model',
        'https://api.example.com',
      );

      expect(snippet).toMatch(/PATCH/);
      expect(snippet).not.toContain('chat.completions.create');
    },
  );

  it.each(['curl', 'python', 'typescript'] as const)(
    'omits a request body for an advertised GET method in %s',
    (language) => {
      const snippet = buildSnippet(
        language,
        { method: 'GET', path: '/v1/custom', type: 'custom' },
        'custom-model',
        'https://api.example.com',
      );

      expect(snippet).toMatch(/GET/);
      expect(snippet).not.toMatch(/(?:-d |json=|JSON\.stringify)/);
    },
  );
});
