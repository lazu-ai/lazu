# Security Policy

## Supported versions

Security fixes land on `main`. If you run a fork or a pinned release, upgrade
to the latest commit before reporting an issue as unfixed.

## Reporting a vulnerability

Please do not open a public issue, discussion or pull request for a suspected
vulnerability.

Report it privately through GitHub's private vulnerability reporting:
<https://github.com/lazu-ai/lazu/security/advisories/new>. Include:

- the affected commit, tag or deployment version;
- a short impact summary;
- reproduction steps or a proof of concept;
- relevant logs, with secrets removed.

We acknowledge reports within three working days and agree a disclosure date
with you once a fix is ready. Issues that affect only the hosted service at
lazu.ai follow the same path.

## Secret handling

Never commit production credentials, API keys, database dumps, local `.env`
files or private service URLs. Use environment variables and deployment
secrets for runtime configuration.

If a secret is committed by accident, rotate it immediately, even after it is
removed from git history.
