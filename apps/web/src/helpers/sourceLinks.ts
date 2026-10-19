// Links derived from the deployment's published source (`site.source_url`).

const GITHUB_REPOSITORY =
  /^https:\/\/github\.com\/([A-Za-z0-9-]+)\/([A-Za-z0-9._-]+?)(?:\.git)?\/?$/;

/** The new-issue chooser of a GitHub repository, or null for any other source
 *  link (another forge, a tarball, a path inside a repository). */
export const githubNewIssueURL = (sourceURL?: string | null): string | null => {
  const match = GITHUB_REPOSITORY.exec(sourceURL?.trim() ?? '');
  if (!match) return null;
  return `https://github.com/${match[1]}/${match[2]}/issues/new/choose`;
};
