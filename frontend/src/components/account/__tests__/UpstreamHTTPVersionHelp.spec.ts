import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import UpstreamHTTPVersionHelp from '../UpstreamHTTPVersionHelp.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('UpstreamHTTPVersionHelp', () => {
  it('shows all protocol choices and the operational safeguards', () => {
    const wrapper = mount(UpstreamHTTPVersionHelp, {
      props: { modelValue: 'auto' }
    })

    expect(wrapper.get('[data-testid="upstream-http-version-help-auto"]').attributes('data-selected'))
      .toBe('true')
    expect(wrapper.get('[data-testid="upstream-http-version-help-http1"]').attributes('data-selected'))
      .toBe('false')
    expect(wrapper.get('[data-testid="upstream-http-version-help-http2"]').attributes('data-selected'))
      .toBe('false')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.scopeHint')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.http1UseWhen')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.decisionHint')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.switchHint')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.emergencyOverrideHint')
  })

  it('highlights the protocol currently selected in the account form', async () => {
    const wrapper = mount(UpstreamHTTPVersionHelp, {
      props: { modelValue: 'auto' }
    })

    await wrapper.setProps({ modelValue: 'http1' })

    expect(wrapper.get('[data-testid="upstream-http-version-help-auto"]').attributes('data-selected'))
      .toBe('false')
    expect(wrapper.get('[data-testid="upstream-http-version-help-http1"]').attributes('data-selected'))
      .toBe('true')
    expect(wrapper.text()).toContain('admin.accounts.upstreamHTTPVersion.currentSelection')
  })
})
