<template>
  <div
    class="rounded-lg border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-600 dark:bg-dark-800/50"
    data-testid="upstream-http-version-help"
  >
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-xs font-semibold text-gray-700 dark:text-gray-200">
        {{ t('admin.accounts.upstreamHTTPVersion.guideTitle') }}
      </p>
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.upstreamHTTPVersion.scopeHint') }}
      </p>
    </div>

    <div class="mt-3 grid gap-2 sm:grid-cols-3">
      <div
        v-for="option in guideOptions"
        :key="option.value"
        :data-testid="`upstream-http-version-help-${option.value}`"
        :data-selected="modelValue === option.value"
        :class="[
          'rounded-md border p-3 transition-colors',
          modelValue === option.value
            ? 'border-primary-400 bg-primary-50 ring-1 ring-primary-200 dark:border-primary-500 dark:bg-primary-900/20 dark:ring-primary-800'
            : 'border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-700/50'
        ]"
      >
        <div class="flex flex-wrap items-center gap-1.5">
          <p class="text-xs font-semibold text-gray-800 dark:text-gray-100">
            {{ option.title }}
          </p>
          <span
            v-if="option.recommended || modelValue === option.value"
            class="rounded-full bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
          >
            {{ badgeText(option.recommended, modelValue === option.value) }}
          </span>
        </div>
        <p class="mt-1.5 text-xs leading-5 text-gray-600 dark:text-gray-300">
          {{ option.description }}
        </p>
        <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">
          <span class="font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.upstreamHTTPVersion.useWhenLabel') }}
          </span>
          {{ option.useWhen }}
        </p>
      </div>
    </div>

    <div class="mt-3 space-y-1 border-t border-gray-200 pt-2 dark:border-dark-600">
      <p class="text-xs font-medium text-gray-600 dark:text-gray-300">
        {{ t('admin.accounts.upstreamHTTPVersion.decisionHint') }}
      </p>
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.upstreamHTTPVersion.switchHint') }}
      </p>
      <p class="text-xs text-amber-600 dark:text-amber-400">
        {{ t('admin.accounts.upstreamHTTPVersion.emergencyOverrideHint') }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { UpstreamHTTPVersion } from '@/types'

defineProps<{
  modelValue: UpstreamHTTPVersion
}>()

const { t } = useI18n()

const guideOptions = computed(() => [
  {
    value: 'auto' as UpstreamHTTPVersion,
    title: t('admin.accounts.upstreamHTTPVersion.autoGuideTitle'),
    description: t('admin.accounts.upstreamHTTPVersion.autoGuideDescription'),
    useWhen: t('admin.accounts.upstreamHTTPVersion.autoUseWhen'),
    recommended: true
  },
  {
    value: 'http1' as UpstreamHTTPVersion,
    title: t('admin.accounts.upstreamHTTPVersion.http1GuideTitle'),
    description: t('admin.accounts.upstreamHTTPVersion.http1GuideDescription'),
    useWhen: t('admin.accounts.upstreamHTTPVersion.http1UseWhen'),
    recommended: false
  },
  {
    value: 'http2' as UpstreamHTTPVersion,
    title: t('admin.accounts.upstreamHTTPVersion.http2GuideTitle'),
    description: t('admin.accounts.upstreamHTTPVersion.http2GuideDescription'),
    useWhen: t('admin.accounts.upstreamHTTPVersion.http2UseWhen'),
    recommended: false
  }
])

function badgeText(recommended: boolean, selected: boolean): string {
  if (recommended && selected) {
    return t('admin.accounts.upstreamHTTPVersion.recommendedAndCurrent')
  }
  return t(
    selected
      ? 'admin.accounts.upstreamHTTPVersion.currentSelection'
      : 'admin.accounts.upstreamHTTPVersion.recommended'
  )
}
</script>
