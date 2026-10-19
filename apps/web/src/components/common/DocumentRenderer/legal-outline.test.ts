import { describe, expect, it } from 'vitest';
import {
  bodyWithoutHeadMatter,
  chapterId,
  outlineOf,
  splitChapterHeading,
  updatedDateOf,
} from './legal-outline';

const PRIVACY = `# 隐私政策

最近更新日期：2026 年 8 月 27 日

Lazu 是由 DUBRIO LLC 提供并运营的服务。

## 一、我们处理的信息

### 1. 账户与身份验证信息

正文。

## 二、我们如何使用信息

正文。

## 十一、联系我们

正文。
`;

describe('splitChapterHeading', () => {
  it('separates a CJK enumerator from its title', () => {
    expect(splitChapterHeading('一、我们处理的信息')).toEqual({
      number: '一',
      title: '我们处理的信息',
    });
    expect(splitChapterHeading('十一、联系我们')).toEqual({
      number: '十一',
      title: '联系我们',
    });
  });

  it('separates an arabic enumerator from its title', () => {
    expect(splitChapterHeading('2. Information we collect')).toEqual({
      number: '2',
      title: 'Information we collect',
    });
  });

  it('leaves an unnumbered heading whole', () => {
    expect(splitChapterHeading('Contact us')).toEqual({
      number: '',
      title: 'Contact us',
    });
  });
});

describe('outlineOf', () => {
  it('preserves nonsequential authored HTML chapter numbers', () => {
    const outline = outlineOf(
      '<h2>5. Data <em>retention</em></h2><h2>9. Contact &amp; support</h2>',
    );
    expect(outline.map((chapter) => chapter.number)).toEqual(['5', '9']);
    expect(outline.map((chapter) => chapter.title)).toEqual([
      'Data retention',
      'Contact & support',
    ]);
  });
  it('decodes entities and tracks repeated headings by their source position', () => {
    const chapters = outlineOf(
      '# Terms\n\n## 1. Data &amp; privacy\n\n## 1. Data &amp; privacy',
    );
    expect(chapters.map((chapter) => chapter.title)).toEqual([
      'Data & privacy',
      'Data & privacy',
    ]);
    expect(chapters[0].id).not.toBe(chapters[1].id);
    expect(chapters[0].line).not.toBe(chapters[1].line);
    expect(outlineOf('<h2>1. Data &amp; privacy</h2>')[0].title).toBe(
      'Data & privacy',
    );
  });

  it('ignores headings inside fenced examples', () => {
    expect(
      outlineOf('```md\n## Example\n```\n\n## Actual chapter').map(
        (chapter) => chapter.title,
      ),
    ).toEqual(['Actual chapter']);
  });
  it('lists every chapter with a stable anchor id', () => {
    const outline = outlineOf(PRIVACY);
    expect(outline.map((chapter) => chapter.number)).toEqual([
      '一',
      '二',
      '十一',
    ]);
    expect(outline.map((chapter) => chapter.title)).toEqual([
      '我们处理的信息',
      '我们如何使用信息',
      '联系我们',
    ]);
    expect(outline[0].id).toBe(chapterId(0, '我们处理的信息'));
    expect(new Set(outline.map((chapter) => chapter.id)).size).toBe(3);
  });

  it('reads h2 headings out of an HTML override', () => {
    const outline = outlineOf(
      '<h1>Privacy</h1><h2>1. Data we collect</h2><p>x</p><h2>2. Retention</h2>',
    );
    expect(outline.map((chapter) => chapter.title)).toEqual([
      'Data we collect',
      'Retention',
    ]);
  });

  it('returns nothing for a document without chapters', () => {
    expect(outlineOf('# Title\n\nJust one paragraph.')).toEqual([]);
  });

  it('stores the words a reader sees, not the inline markdown', () => {
    // The rendered heading is matched back to this entry by its text, so the
    // outline has to hold the same plain text react-markdown will produce.
    const outline = outlineOf('## 1. **Eligibility**\n\n## 2. `API` keys');
    expect(outline.map((chapter) => chapter.title)).toEqual([
      'Eligibility',
      'API keys',
    ]);
    // An underscore inside a word is not emphasis and survives.
    expect(outlineOf('## 3. user_id handling')[0].title).toBe(
      'user_id handling',
    );
  });
});

describe('updatedDateOf', () => {
  it('reads the published date out of the prose line', () => {
    expect(updatedDateOf(PRIVACY)).toBe('2026 年 8 月 27 日');
    expect(updatedDateOf('Last updated: June 1, 2026\n\nBody')).toBe(
      'June 1, 2026',
    );
  });

  it('reads the Japanese label the bundled ja documents use', () => {
    // internal/setting/system_setting/legal_defaults/*.ja.md say 最終更新日.
    expect(
      updatedDateOf('# 個人情報保護方針\n\n最終更新日：2026 年 8 月 27 日\n'),
    ).toBe('2026 年 8 月 27 日');
  });

  it('is empty when the document states no date', () => {
    expect(updatedDateOf('# Terms\n\nBody')).toBe('');
  });
});

describe('bodyWithoutHeadMatter', () => {
  it('removes the HTML update paragraph while preserving the document body', () => {
    expect(
      bodyWithoutHeadMatter(
        '<h1>Privacy</h1><p>Last updated: June 1, 2026</p><h2>Data</h2><p>Body</p>',
      ),
    ).toBe('<h2>Data</h2><p>Body</p>');
  });
  it('drops the title and date the page head already shows', () => {
    const body = bodyWithoutHeadMatter(PRIVACY);
    expect(body.startsWith('Lazu 是由')).toBe(true);
    expect(body).not.toContain('# 隐私政策');
    expect(body).not.toContain('最近更新日期');
    // Everything else survives untouched.
    expect(body).toContain('## 一、我们处理的信息');
    expect(body).toContain('### 1. 账户与身份验证信息');
  });

  it('drops the Japanese date line too', () => {
    const body = bodyWithoutHeadMatter(
      '# 個人情報保護方針\n\n最終更新日：2026 年 8 月 27 日\n\n本文。\n',
    );
    expect(body).not.toContain('最終更新日');
    expect(body).toContain('本文。');
  });

  it('drops a leading h1 from an HTML override', () => {
    expect(bodyWithoutHeadMatter('<h1>Privacy</h1><p>Body</p>')).toBe(
      '<p>Body</p>',
    );
  });
});
