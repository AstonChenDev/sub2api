import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(__dirname, '../HuggingFacePoolsView.vue'), 'utf8')

describe('HuggingFacePoolsView credential diagnostics', () => {
  it('renders a structured upstream status code and the stored error message', () => {
    expect(source).toContain("t('admin.huggingface.errorCode'")
    expect(source).toContain("t('admin.huggingface.errorMessage'")
    expect(source).toContain('item.upstream_status_code')
    expect(source).toContain('HTTP {{ item.upstream_status_code }}')
    expect(source).toContain("item.error_message || credentialReasonLabel(item.disabled_reason) || '-'")
  })

  it('uses readable lifecycle labels instead of exposing only the raw error status', () => {
    expect(source).toContain("item.status === 'error'")
    expect(source).toContain("t('admin.huggingface.statusError')")
    expect(source).toContain("case 'billing_required'")
    expect(source).toContain("t('admin.huggingface.reasonBillingRequired')")
  })
})
