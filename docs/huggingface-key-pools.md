# Hugging Face dedicated key pools

Hugging Face routing is an opt-in, isolated extension. A client still sends one
normal Sub2API API key. That API key belongs to a `huggingface` group, and the
gateway performs two bounded scheduling stages:

1. Select the lowest-priority-number matching upstream pool. Pools at the same
   priority use smooth weighted round-robin.
2. Rotate a bounded Redis ZSET window of credentials from that pool. The total
   credential count does not change steady-state request selection work. A
   missing Redis index is rebuilt once under singleflight and may scan the full
   pool before steady-state selection resumes.

HF credentials are stored in `accounts` only to reuse concurrency, usage and
billing foreign keys. They are never inserted into `account_groups`, and a
database trigger rejects an accidental binding. The legacy scheduler therefore
does not load these credentials into its full account snapshot.

## Enable

The feature is disabled by default. Generate and persist a dedicated key:

```bash
openssl rand -hex 32
```

Configure:

```yaml
huggingface:
  enabled: true
  encryption_key: "<64 hexadecimal characters>"
  allowed_base_urls:
    - "https://router.huggingface.co"
```

Do not rotate or lose `encryption_key` without first migrating stored
credentials. Equal tokens are deduplicated with a keyed HMAC; token plaintext is
encrypted with AES-256-GCM and is never returned by an API.

## Configure pools

1. Create an admin group whose platform is `huggingface`.
2. Open **HF Key Pools** in the admin sidebar.
3. Create one or more pools and specify exact models or wildcard patterns such
   as `meta-llama/*`.
4. Import up to 100,000 `hf_...` tokens per request. Import requires step-up
   authentication.
5. Assign a normal Sub2API API key to the Hugging Face group.

Pool priority is strict: lower values are exhausted first. Weight only balances
pools at the same priority. Key priority follows the same lower-is-better rule.

The public endpoints are the existing OpenAI-compatible endpoints:

- `POST /v1/chat/completions`
- `POST /v1/responses` (root endpoint only; bridged to Chat Completions)
- `POST /v1/messages` (bridged to Chat Completions)
- `POST /v1/messages/count_tokens` (estimated locally; no HF token is sent)
- `GET /v1/models` (lists configured exact model names; wildcard patterns
  cannot be enumerated)

Responses subpaths and WebSocket, embeddings, image, video and live endpoints
are not enabled for Hugging Face pools. HF bearer requests do not follow HTTP
redirects, preventing a token from being forwarded to a redirect target.

## Credential lifecycle

- `401`: permanently disables the token.
- `403`: permanently disables the token for insufficient permission.
- `402` with the Hugging Face monthly-included-credit exhaustion message:
  disables the token until day 1 of the next month at the configured local hour.
- Other `402`: temporary billing cooldown.
- `429`: honors `Retry-After`, clamped by configuration, otherwise uses the
  configured rate-limit cooldown.
- `5xx` and transport failures: temporary per-token cooldown and pool circuit
  breaker accounting.
- Success clears the pool failure streak.

The recovery worker handles up to 100,000 due monthly credentials per cycle and
publishes one atomic Redis index generation per affected pool. A periodic full
reconciliation repairs cache loss, and request-time missing-index rebuilds are
singleflight-coalesced.

## Admin API

All routes are under `/api/v1/admin/huggingface`:

- `GET|POST /pools`
- `GET|PUT|DELETE /pools/:pool_id`
- `GET|POST /pools/:pool_id/credentials`
- `POST /pools/:pool_id/credentials/:account_id/recover`
- `DELETE /pools/:pool_id/credentials/:account_id`
- `POST /pools/:pool_id/reconcile`
- `POST /recover-due`

The credential import body is:

```json
{
  "credentials": [
    { "token": "hf_...", "priority": 50, "concurrency": 1 }
  ]
}
```
