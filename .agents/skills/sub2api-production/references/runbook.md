# Sub2API production runbook

## Topology and invariants

- Public URL: `https://sub2api.xingyuexiezuo.com`.
- The BaoTa Nginx gateway and Docker application stack run on the same production host.
- SSH alias expected on each operator workstation: `sub2api-prod`.
- Production checkout: `/www/wwwroot/sub2api`, branch `custom`.
- Deploy only commits already merged and pushed to `origin/custom`.
- Operations root: `/opt/sub2api`.
- Persistent environment: `/opt/sub2api/.env`, mode `600`.
- Application Compose: `/opt/sub2api/app-compose.yml`.
- Infrastructure Compose: `/opt/sub2api/infra-compose.yml`.
- Blue/green ports: `18080` and `18081`, bound to loopback.
- Current active slot source of truth: `/opt/sub2api/nginx/active-upstream.conf`.
- Release entrypoint: `/opt/sub2api/bin/deploy-release.sh`.
- Rollback switcher: `/opt/sub2api/bin/switch-upstream.sh`.
- Nginx vhost: `/www/server/panel/vhost/nginx/sub2api.xingyuexiezuo.com.conf`.
- Nginx validation: `/www/server/nginx/sbin/nginx -t -c /www/server/nginx/conf/nginx.conf`.
- Nginx logs: `/www/wwwlogs/sub2api.xingyuexiezuo.com.log` and `/www/wwwlogs/sub2api.xingyuexiezuo.com.error.log`.

Never infer the live slot from history. Read the active-upstream file and inspect established connections on every operation.

## Read-only inspection

Use `bash tools/codex/prod.sh status`. It reports the host, time, disk space, active upstream, established application-port connections, production Git state, container health, Nginx syntax, and public health without displaying environment variables or secrets.

If direct follow-up inspection is necessary, keep output bounded and redact authorization headers, cookies, query signatures, user content, request bodies, upstream credentials, and database values.

## Required release sequence

1. Finish and test the feature locally. Commit it, push it, and merge it into `origin/custom`.
2. Fetch `origin/custom` locally and resolve its full SHA. The requested deployment SHA must equal that remote-tracking SHA.
3. Run the read-only production status and examine the hostname, time, Docker health, disk space, active upstream, established connections, production branch, worktree state, and current revision.
4. Run `bash tools/codex/prod.sh deploy <full-sha> --confirm-production`. The wrapper stops on a dirty checkout, unexpected branch, or mismatch between the requested SHA and production's freshly fetched `origin/custom`.
5. The server release script builds the exact production `HEAD`, replaces only the drained inactive slot, waits for container health, checks the local health endpoint, atomically switches the Nginx include, validates Nginx, and reloads gracefully.
6. If it reports established connections on the inactive target port, leave that old container running and stop. Retry only after it drains and only if the user still wants the deployment.
7. Keep the previously live container running after the switch. It is the rollback target while old workers and long-lived requests drain.
8. Validate the active slot and release label, both container health states, Nginx syntax, public `/health`, the feature in scope, recent application logs, and recent Nginx errors. Confirm traffic reaches the requested revision.

Do not run a second deployment merely to make both slots identical.

## Rollback

Rollback is not part of the generic wrapper because the operator must first identify a known healthy previous container and its exact slot, port, and revision. With an explicit rollback request:

1. Inspect both containers and the active-upstream file.
2. Verify the previous container's local health endpoint.
3. Back up the active-upstream include without exposing its contents unnecessarily.
4. Run `/opt/sub2api/bin/switch-upstream.sh <previous-port> <slot> <revision>` with the verified values.
5. Validate Nginx syntax, public health, application behavior, and logs.
6. Keep the failed release for incident analysis.

## Prohibited release shortcuts

- No `git reset --hard`, forced update, or deployment from another branch.
- No `docker compose down`, volume deletion, database drop, Redis flush, or test-data restore.
- Do not stop the live or previously live container simply to free a slot.
- Do not edit Nginx or the active-upstream include without a backup and syntax validation.
- Back up `/opt/sub2api/.env` with mode `600` before an explicitly requested runtime configuration change.
- Preserve `HUGGINGFACE_ENCRYPTION_KEY`; changing or losing it makes imported Hugging Face credentials unreadable.
- Hugging Face credential import is a separate production operation. Never infer permission to import or synchronize credentials from permission to deploy code.
