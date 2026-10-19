import { describe, expect, it } from 'vitest';
import { githubNewIssueURL } from './sourceLinks';

describe('githubNewIssueURL', () => {
  it('points a GitHub repository at its issue chooser', () => {
    const want = 'https://github.com/lazu-ai/lazu/issues/new/choose';
    expect(githubNewIssueURL('https://github.com/lazu-ai/lazu')).toBe(want);
    expect(githubNewIssueURL('https://github.com/lazu-ai/lazu/')).toBe(want);
    expect(githubNewIssueURL(' https://github.com/lazu-ai/lazu.git ')).toBe(
      want,
    );
  });

  it('offers nothing without a GitHub repository link', () => {
    expect(githubNewIssueURL(undefined)).toBeNull();
    expect(githubNewIssueURL('')).toBeNull();
    expect(githubNewIssueURL('https://gitlab.com/acme/lazu')).toBeNull();
    expect(
      githubNewIssueURL('https://github.com/lazu-ai/lazu/tree/main'),
    ).toBeNull();
    expect(githubNewIssueURL('http://github.com/lazu-ai/lazu')).toBeNull();
  });
});
