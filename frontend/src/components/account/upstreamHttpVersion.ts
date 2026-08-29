import type { AccountPlatform, AccountType, UpstreamHTTPVersion } from '@/types'

export const UPSTREAM_HTTP_VERSION_AUTO: UpstreamHTTPVersion = 'auto'

// Only account paths that use the shared OpenAI-compatible HTTP transport can
// override its protocol. Grok and Hugging Face use dedicated transports.
export function isUpstreamHTTPVersionCapable(
  platform: AccountPlatform,
  type: AccountType
): boolean {
  if (platform === 'openai') {
    return type === 'oauth' || type === 'setup-token' || type === 'apikey'
  }
  return (
    type === 'apikey' &&
    (platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek')
  )
}

export function normalizeUpstreamHTTPVersion(value: unknown): UpstreamHTTPVersion {
  return value === 'http1' || value === 'http2' ? value : UPSTREAM_HTTP_VERSION_AUTO
}
