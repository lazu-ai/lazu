import { describe, expect, it } from 'vitest';
import { chatTemplateLeaksKey, chatTemplateNeedsKey } from './chatTemplate';

describe('chat templates', () => {
  it('knows which templates actually need the Key', () => {
    expect(chatTemplateNeedsKey('cherrystudio://providers?data={key}')).toBe(
      true,
    );
    expect(
      chatTemplateNeedsKey('https://chat.example.com/#{cherryConfig}'),
    ).toBe(true);
    expect(chatTemplateNeedsKey('fluent')).toBe(true);
    // Only the gateway address: opening it spends no reveal.
    expect(
      chatTemplateNeedsKey('https://chat.example.com/?host={address}'),
    ).toBe(false);
    expect(chatTemplateNeedsKey('https://chat.example.com/')).toBe(false);
  });

  it('refuses an https template that carries the Key', () => {
    expect(chatTemplateLeaksKey('https://chat.example.com/?token={key}')).toBe(
      true,
    );
    expect(
      chatTemplateLeaksKey('https://cherry.example.com/#/{cherryConfig}'),
    ).toBe(true);
    // Scheme case is the administrator's business, not a way around this.
    expect(chatTemplateLeaksKey('HTTPS://chat.example.com/?token={key}')).toBe(
      true,
    );
  });

  it.each([
    '//chat.example.com/?key={key}',
    '/chat?key={key}',
    'https:\n//chat.example.com/{key}',
    'javascript:alert("{key}")',
  ])('blocks browser destinations: %s', (url) => {
    expect(chatTemplateLeaksKey(url)).toBe(true);
  });

  it('keeps the ones that hand the Key to an installed app', () => {
    expect(chatTemplateLeaksKey('cherrystudio://providers?data={key}')).toBe(
      false,
    );
    expect(chatTemplateLeaksKey('opencat://team/join?token={key}')).toBe(false);
    expect(chatTemplateLeaksKey('fluent')).toBe(false);
  });

  it('keeps an https template that carries no Key', () => {
    expect(
      chatTemplateLeaksKey('https://chat.example.com/?host={address}'),
    ).toBe(false);
    expect(chatTemplateLeaksKey('')).toBe(false);
    expect(chatTemplateLeaksKey(undefined)).toBe(false);
  });
});
