#!/usr/bin/env bash

set -u

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
failures=0

check_command() {
  local command_name="$1"
  if command -v "$command_name" >/dev/null 2>&1; then
    printf 'ok      %s\n' "$command_name"
  else
    printf 'missing %s\n' "$command_name"
    failures=$((failures + 1))
  fi
}

printf 'Sub2API workstation doctor\n'
printf 'repo    %s\n' "$repo_root"

for command_name in git go node pnpm make curl ssh; do
  check_command "$command_name"
done

if command -v docker >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    printf 'ok      docker daemon\n'
  else
    printf 'warning docker is installed but the daemon is unavailable\n'
  fi
else
  printf 'missing docker\n'
  failures=$((failures + 1))
fi

if command -v codex >/dev/null 2>&1; then
  printf 'ok      codex CLI\n'
else
  printf 'info    codex CLI not found; the desktop app or IDE extension can still use repository instructions\n'
fi

if git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'ok      Git worktree\n'
else
  printf 'missing Git worktree\n'
  failures=$((failures + 1))
fi

if [[ -s "$repo_root/AGENTS.md" ]] && [[ -s "$repo_root/.agents/skills/sub2api-production/SKILL.md" ]]; then
  printf 'ok      Codex repository context\n'
else
  printf 'missing Codex repository context\n'
  failures=$((failures + 1))
fi

if ssh -o BatchMode=yes -o ConnectTimeout=5 sub2api-prod true >/dev/null 2>&1; then
  printf 'ok      production SSH alias (key or agent authentication)\n'
else
  printf 'info    production SSH alias is unavailable with non-interactive key authentication\n'
fi

if (( failures > 0 )); then
  printf 'result  %d required prerequisite(s) missing\n' "$failures"
  exit 1
fi

printf 'result  required local prerequisites are available\n'
