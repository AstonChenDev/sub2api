#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ssh_target="${SUB2API_PROD_SSH_TARGET:-sub2api-prod}"
keychain_service="${SUB2API_PROD_KEYCHAIN_SERVICE:-codex-sub2api-prod-ssh}"
keychain_account="${SUB2API_PROD_KEYCHAIN_ACCOUNT:-root}"
auth_mode=""
prod_password=""

cleanup() {
  unset prod_password SSHPASS
}
trap cleanup EXIT HUP INT TERM

usage() {
  cat <<'EOF'
Usage:
  bash tools/codex/prod.sh status
  bash tools/codex/prod.sh deploy <full-origin-custom-sha> --confirm-production

Environment overrides:
  SUB2API_PROD_SSH_TARGET        SSH alias (default: sub2api-prod)
  SUB2API_PROD_KEYCHAIN_SERVICE macOS password fallback service
  SUB2API_PROD_KEYCHAIN_ACCOUNT macOS password fallback account
EOF
}

configure_auth() {
  if ssh -o BatchMode=yes -o ConnectTimeout=8 "$ssh_target" true >/dev/null 2>&1; then
    auth_mode="ssh"
    return
  fi

  if [[ "$(uname -s)" == "Darwin" ]] && command -v security >/dev/null 2>&1 && command -v sshpass >/dev/null 2>&1; then
    if prod_password="$(security find-generic-password -a "$keychain_account" -s "$keychain_service" -w 2>/dev/null)" && [[ -n "$prod_password" ]]; then
      auth_mode="sshpass"
      return
    fi
  fi

  printf 'Unable to authenticate to SSH target %s. Configure the alias with a personal key or approved credential store.\n' "$ssh_target" >&2
  exit 2
}

run_ssh() {
  if [[ "$auth_mode" == "ssh" ]]; then
    ssh "$ssh_target" "$@"
  else
    SSHPASS="$prod_password" sshpass -e ssh "$ssh_target" "$@"
  fi
}

remote_status() {
  run_ssh bash -s <<'REMOTE'
set -euo pipefail

printf '%s\n' '== host =='
hostname
date -Is

printf '%s\n' '== disk =='
df -h / /opt 2>/dev/null | awk 'NR == 1 || !seen[$6]++'

printf '%s\n' '== active upstream =='
sed -n '1,40p' /opt/sub2api/nginx/active-upstream.conf

printf '%s\n' '== established app-port connections =='
if command -v ss >/dev/null 2>&1; then
  ss -Htan state established | awk '$4 ~ /:(18080|18081)$/ {count[$4]++} END {for (address in count) print address, count[address]}'
else
  printf '%s\n' 'ss unavailable'
fi

printf '%s\n' '== production checkout =='
git -C /www/wwwroot/sub2api branch --show-current
git -C /www/wwwroot/sub2api status --short
git -C /www/wwwroot/sub2api rev-parse HEAD

printf '%s\n' '== application containers =='
docker ps --filter 'name=sub2api-blue-' --filter 'name=sub2api-green-' --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'

printf '%s\n' '== nginx syntax =='
/www/server/nginx/sbin/nginx -t -c /www/server/nginx/conf/nginx.conf

printf '%s\n' '== public health =='
curl -fsS --max-time 15 https://sub2api.xingyuexiezuo.com/health
printf '\n'
REMOTE
}

deploy_release() {
  local expected_sha="$1"

  if [[ ! "$expected_sha" =~ ^[0-9a-f]{40}$ ]]; then
    printf 'Deployment requires the full 40-character Git SHA from origin/custom.\n' >&2
    exit 2
  fi

  git -C "$repo_root" fetch origin custom
  local origin_custom_sha
  origin_custom_sha="$(git -C "$repo_root" rev-parse origin/custom)"
  if [[ "$origin_custom_sha" != "$expected_sha" ]]; then
    printf 'Requested SHA does not equal the freshly fetched origin/custom SHA.\n' >&2
    printf 'requested: %s\norigin/custom: %s\n' "$expected_sha" "$origin_custom_sha" >&2
    exit 3
  fi

  configure_auth
  remote_status

  run_ssh bash -s -- "$expected_sha" <<'REMOTE'
set -euo pipefail

expected_sha="$1"
checkout=/www/wwwroot/sub2api

if [[ -n "$(git -C "$checkout" status --porcelain)" ]]; then
  printf '%s\n' 'Production checkout is dirty; refusing deployment.' >&2
  exit 10
fi

if [[ "$(git -C "$checkout" branch --show-current)" != "custom" ]]; then
  printf '%s\n' 'Production checkout is not on custom; refusing deployment.' >&2
  exit 11
fi

git -C "$checkout" fetch origin custom
remote_sha="$(git -C "$checkout" rev-parse origin/custom)"
if [[ "$remote_sha" != "$expected_sha" ]]; then
  printf '%s\n' 'Production origin/custom does not match the requested SHA; refusing deployment.' >&2
  exit 12
fi

git -C "$checkout" merge --ff-only origin/custom
if [[ "$(git -C "$checkout" rev-parse HEAD)" != "$expected_sha" ]]; then
  printf '%s\n' 'Production HEAD does not match the requested SHA after fast-forward.' >&2
  exit 13
fi

/opt/sub2api/bin/deploy-release.sh
REMOTE

  remote_status
}

command_name="${1:-}"
case "$command_name" in
  status)
    [[ $# -eq 1 ]] || { usage >&2; exit 2; }
    configure_auth
    remote_status
    ;;
  deploy)
    [[ $# -eq 3 && "$3" == "--confirm-production" ]] || { usage >&2; exit 2; }
    deploy_release "$2"
    ;;
  -h|--help|help|'')
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
