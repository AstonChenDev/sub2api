---
name: sub2api-production
description: Inspect, upgrade, deploy, validate, or roll back the Sub2API production environment. Use for production status, SSH, release, blue-green deployment, health checks, runtime configuration, or rollback; do not use for local-only development.
---

# Sub2API production operations

Read [references/runbook.md](references/runbook.md) completely before any production operation.

## Entry points

- Workstation and access check: `bash tools/codex/doctor.sh`
- Read-only production status: `bash tools/codex/prod.sh status`
- Deploy an exact merged commit: `bash tools/codex/prod.sh deploy <full-origin-custom-sha> --confirm-production`

Use these maintained entry points instead of reimplementing SSH authentication or the release sequence in ad hoc shell commands.

## Authorization boundary

- Read-only inspection may run when needed to answer the user's production question and access is already configured.
- Deployment requires an explicit request to deploy the exact change in scope.
- Rollback, runtime configuration changes, credential imports, database/Redis writes, or administrator API writes each require their own explicit matching request.
- If the requested commit is not merged and pushed to `origin/custom`, finish or report that prerequisite; do not bypass it.

Stop rather than repair around a dirty production checkout, an unexpected branch, Git divergence, an unhealthy target, an occupied inactive slot, an unknown active slot, or failed public verification.
