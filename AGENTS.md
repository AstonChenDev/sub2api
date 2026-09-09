# Sub2API repository instructions

These instructions are shared by everyone who opens this repository with Codex. Keep machine-specific paths, credentials, private keys, tokens, and production data out of this file and out of Git.

## Project map

- `backend/`: Go API service using Gin and Ent. The supported Go version is declared in `backend/go.mod`.
- `frontend/`: Vue 3 and TypeScript client. Use pnpm; do not use npm to install dependencies.
- `deploy/`: generic self-hosting assets and deployment tests. These are not the production blue/green control files.
- `docs/`: feature and operator documentation.
- `skills/sub2api-admin/`: the reusable administrator API CLI implementation and detailed command reference.
- `.agents/skills/`: repository-scoped Codex workflows, loaded automatically by Codex.
- `DEV_GUIDE.md`: development setup, repository structure, and common failure modes.
- `TEAM_CODEX.md`: teammate onboarding for Codex and production access.

## How to investigate questions

- Base answers about current behavior on repository code, tests, migrations, configuration examples, and tracked docs. Distinguish confirmed behavior from inference.
- Search with `rg` or `rg --files` first. Follow a request from the HTTP route through handler, service, repository, schema/migration, and frontend caller when applicable.
- Do not treat a historical deployment state, a local cache, or a generated file as the current production state.
- For administrator API operations, use the `sub2api-admin` repository skill and its bundled CLI instead of improvised `curl` commands.
- For production status, upgrades, deployments, or rollback, use the `sub2api-production` repository skill.

## Development workflow

- Preserve unrelated and pre-existing working-tree changes. Never discard or overwrite them to make a task easier.
- Use focused tests while iterating, then run checks proportional to the changed area.
- Backend checks:
  - Unit: `make -C backend test-unit`
  - Integration: `make -C backend test-integration`
  - All Go tests and lint: `make -C backend test`
  - Generate Ent and Wire output after changing schemas or dependency wiring: `make -C backend generate`
- Frontend checks:
  - Install: `pnpm --dir frontend install --frozen-lockfile`
  - Lint: `pnpm --dir frontend run lint:check`
  - Type check: `pnpm --dir frontend run typecheck`
  - Tests: `pnpm --dir frontend run test:run`
  - Build: `pnpm --dir frontend run build`
- Repository-level checks:
  - Critical backend and frontend checks: `make test`
  - Build both applications: `make build`
- If `frontend/package.json` changes, keep `frontend/pnpm-lock.yaml` synchronized.
- If a Go interface changes, locate and update all implementations and test doubles.
- Never hand-edit generated Ent or Wire output when the generator is the source of truth.

## Git and release branches

- `origin` is the company fork. `upstream` is the read-only upstream project.
- Production releases come only from `origin/custom`.
- Do not deploy an uncommitted workspace, a local-only commit, another branch, or a commit that has not been pushed and merged into `origin/custom`.
- Do not push, merge, tag, publish a release, or deploy unless the user explicitly requests that external mutation.

## Secrets and production safety

- Never print, copy, commit, or persist production passwords, SSH private keys, administrator tokens, API keys, signed URLs, database contents, or decrypted credentials.
- Each teammate must receive individual GitHub and production access. Prefer individual SSH keys and an auditable account or sudo path; do not share another person's private key or macOS Keychain item.
- Environment files and runtime credentials remain outside the repository. Example files must contain placeholders only.
- Production database and Redis are persistent state. Never run `docker compose down`, remove volumes, drop databases, flush Redis, copy test data over production, or stop the old live container during a normal release.
- Read-only production inspection is allowed when it answers the user's request and access is already configured. A deployment, rollback, configuration write, credential import, or administrator API write requires an explicit matching user request.
- Before any production mutation, resolve the exact target commit and current live slot with read-only checks. Stop on a dirty production checkout, unexpected branch, divergence, unavailable health check, or ambiguous target.

## Review priorities

- Prioritize authentication and authorization boundaries, credential exposure, billing correctness, account scheduling, concurrency, rate limiting, migrations, backward compatibility, and safe upgrade behavior.
- Require tests for bug fixes and behavior changes where a regression can be reproduced deterministically.
- Treat logging of authorization headers, cookies, user prompts, upstream responses, credentials, or decrypted payloads as a security defect.
