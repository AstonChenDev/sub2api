---
name: sub2api-admin
description: Inspect or manage Sub2API administrator APIs, including accounts, groups, proxies, redeem codes, imports, exports, and backend settings. Use for Sub2API admin operations; do not use for SSH deployment or ordinary code changes.
---

# Sub2API administrator API

Use the maintained repository implementation at `skills/sub2api-admin/`.

1. Read `skills/sub2api-admin/SKILL.md` completely.
2. Read `skills/sub2api-admin/references/admin-cli.md` when command or payload details are needed.
3. Run the CLI from `skills/sub2api-admin/` so its relative script paths resolve correctly.
4. Keep `SUB2API_BASE_URL` and authentication in the environment. Never echo or persist their values.
5. Start with a read-only command. Require an explicit matching user request before any write, import, bulk update, export containing credentials, or deletion.
6. Resolve and show non-secret target identifiers before a write, then perform a follow-up read to verify it.

Do not improvise direct database mutations as a substitute for a supported administrator API operation.
