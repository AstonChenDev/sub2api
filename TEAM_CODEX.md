# Sub2API teammate Codex setup

This repository carries its project knowledge in `AGENTS.md` and its repeatable workflows in `.agents/skills/`. A teammate who clones the repository and opens its root in Codex receives the same project-level guidance automatically. Personal credentials and machine configuration are deliberately not included.

## 1. Access to provision

The project owner or infrastructure administrator should provision these independently for each teammate:

1. Read/write access to `git@github.com:AstonChenDev/sub2api.git` as appropriate for the teammate's role.
2. A personal SSH key or auditable SSH account for production. Do not copy another teammate's private key or export their Keychain password.
3. A Sub2API administrator API key only when the teammate must perform administrator API operations.
4. Access to any external credential source, database, log service, or MCP connector only when their role requires it.

Production access and administrator API access are separate capabilities. A developer can investigate and change code without either one.

## 2. Workstation prerequisites

- Codex desktop app, CLI, or IDE extension, signed in with the teammate's own account.
- Git and repository access.
- Go version matching `backend/go.mod`.
- Node.js 20 and pnpm 9.
- Docker for local integration work.
- `make`, `curl`, and OpenSSH.

Run the repository doctor after cloning:

```bash
bash tools/codex/doctor.sh
```

The doctor is read-only. It reports missing local tools and whether the production SSH alias is usable; it never reads or prints a password.

## 3. Clone and verify Codex context

```bash
git clone git@github.com:AstonChenDev/sub2api.git
cd sub2api
git checkout custom
```

Start a new Codex task from the repository root and ask:

```text
请总结当前项目指令、可用的 Sub2API 仓库技能，以及修改后端和前端分别要运行的检查。
```

Codex should identify `AGENTS.md`, `sub2api-admin`, and `sub2api-production`. Codex reads the instruction chain at the start of a task, so restart the task after changing `AGENTS.md` or repository skill metadata.

## 4. Configure production SSH

The production workflow expects an SSH host alias named `sub2api-prod`. The current server-side release tools require an account with direct access to Docker, Nginx, `/www/wwwroot/sub2api`, and `/opt/sub2api`; today that account is `root`. The infrastructure administrator should add each teammate's distinct public key and revoke it individually when access ends. A typical personal `~/.ssh/config` entry is:

```sshconfig
Host sub2api-prod
  HostName sub2api.xingyuexiezuo.com
  User root
  IdentityFile ~/.ssh/<issued-private-key>
  IdentitiesOnly yes
```

Then verify without changing production:

```bash
ssh sub2api-prod true
bash tools/codex/prod.sh status
```

For the existing macOS password-based fallback, a teammate may store an independently issued password in Keychain under service `codex-sub2api-prod-ssh` and their SSH account name. SSH keys are preferred because they are revocable per person and provide a clearer audit trail.

## 5. Configure administrator API access

Set secrets only in the current shell, a password manager integration, or another approved secret store:

```bash
export SUB2API_BASE_URL='https://sub2api.xingyuexiezuo.com'
export SUB2API_ADMIN_API_KEY='<personal-admin-api-key>'
```

Do not add these values to `.env`, shell scripts, Codex memories, chat messages, or Git. Ask Codex to use `$sub2api-admin` for account, group, redeem-code, proxy, or administrator settings work.

## 6. Normal requests

Examples that need only the repository:

```text
解释账号调度从路由到数据库查询的完整链路，并给出文件和行号。
修复这个接口问题，运行相关测试，但不要提交或部署。
对比当前 custom 分支与 upstream/main 的差异和潜在冲突。
```

Examples that use configured production access:

```text
只读检查生产环境当前版本、活动槽位、容器健康和公网 /health，不要修改任何内容。
把已经合并到 origin/custom 的提交 <sha> 部署到生产，严格按蓝绿流程验证；遇到脏工作区、分叉或占用中的备用槽立即停止。
```

Code changes, Git push/merge, production deployment, rollback, configuration changes, credential imports, and administrator API writes are separate actions. State each external mutation explicitly in the request.

## 7. Offboarding

When a teammate leaves the project, revoke their GitHub access, SSH key/account, administrator API key, and external connector access independently. No repository change should be needed because personal credentials are never committed.
