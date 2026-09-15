# Security Policy

`vibe` is a local-first agent that can read files, run shell commands, and make
network requests. Please report vulnerabilities responsibly.

## Supported versions

Fixes are shipped for the latest release on the `main` branch. Older releases may
not receive backports.

| Version | Supported |
| ------- | --------- |
| Latest release | Yes |
| `main` (development) | Best effort |
| Older releases | No |

## Reporting a vulnerability

**Do not open a public issue, discussion, or pull request for a security issue.**

Use GitHub's private reporting flow:

1. Open a [private security advisory](https://github.com/jonathanhecl/vibe-coder/security/advisories/new).
2. Include, when possible:
   - A description of the issue and its impact.
   - The affected version (`vibe --version`) and platform.
   - Reproduction steps or a minimal proof of concept.
   - Any suggested fix or mitigation.

If you cannot use GitHub advisories, email the maintainer at
**jonathanhecl@gmail.com** with `[vibe-coder security]` in the subject line.

You will receive an acknowledgement as soon as possible, and a status update once
the report is triaged. Please give the maintainer a reasonable window to release a
fix before any public disclosure.

## Scope

Examples of in-scope issues:

- Sandbox or permission bypasses (dangerous-command blocklist, protected paths,
  plan/review mode restrictions).
- SSRF bypasses in `WebFetch` / `WebSearch` (redirect revalidation, DNS
  rebinding, private-address handling).
- Path traversal, symlink, or checkpoint (`GitUndo`) escapes.
- Secret leakage through shell tools, logs, the run log, or session persistence.
- Supply-chain issues in the build and release scripts.

Out of scope: vulnerabilities in Ollama or the models themselves, and issues that
require an attacker to already control the local machine or the model output you
chose to run.
