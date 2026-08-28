<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('admin.huggingface.title', 'Hugging Face Key Pools') }}
          </h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.huggingface.description', 'Dedicated bounded scheduling for large Hugging Face token pools. These credentials never enter the legacy account scheduler.') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <select v-model.number="selectedGroupId" class="input min-w-56" :disabled="groupsLoading">
            <option :value="0">{{ t('admin.huggingface.selectGroup', 'Select a Hugging Face group') }}</option>
            <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
          </select>
          <button class="btn btn-secondary" :disabled="loading || !selectedGroupId" @click="loadPools">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button class="btn btn-primary" :disabled="!selectedGroupId" @click="openCreatePool">
            <Icon name="plus" size="md" class="mr-2" />
            {{ t('admin.huggingface.createPool', 'Create pool') }}
          </button>
        </div>
      </div>

      <div v-if="!groupsLoading && groups.length === 0" class="card p-8 text-center">
        <p class="font-medium text-gray-900 dark:text-white">{{ t('admin.huggingface.noGroup', 'No Hugging Face group exists') }}</p>
        <p class="mt-1 text-sm text-gray-500">{{ t('admin.huggingface.noGroupHint', 'Create a group with platform Hugging Face first, then create one or more upstream pools here.') }}</p>
        <RouterLink to="/admin/groups" class="btn btn-primary mt-4 inline-flex">{{ t('nav.groups', 'Groups') }}</RouterLink>
      </div>

      <div v-else-if="loading" class="card flex justify-center p-12">
        <LoadingSpinner />
      </div>

      <div v-else-if="selectedGroupId && pools.length === 0" class="card p-10 text-center text-sm text-gray-500">
        {{ t('admin.huggingface.noPools', 'No upstream pool is configured for this group.') }}
      </div>

      <div v-else class="grid gap-4 xl:grid-cols-2">
        <article v-for="pool in pools" :key="pool.id" class="card overflow-hidden">
          <div class="h-1 bg-gradient-to-r from-amber-400 to-yellow-500"></div>
          <div class="space-y-4 p-5">
            <div class="flex items-start justify-between gap-4">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2 class="truncate text-lg font-semibold text-gray-900 dark:text-white">{{ pool.name }}</h2>
                  <span :class="pool.status === 'active' ? 'badge badge-success' : 'badge badge-gray'">{{ pool.status }}</span>
                </div>
                <p class="mt-1 truncate text-xs text-gray-500" :title="pool.base_url">{{ pool.base_url }}</p>
              </div>
              <div class="flex items-center gap-1">
                <button class="btn btn-ghost btn-sm" :title="t('common.edit', 'Edit')" @click="openEditPool(pool)"><Icon name="edit" size="sm" /></button>
                <button class="btn btn-ghost btn-sm text-red-600" :title="t('common.delete', 'Delete')" @click="removePool(pool)"><Icon name="trash" size="sm" /></button>
              </div>
            </div>

            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800"><p class="text-xs text-gray-500">{{ t('admin.huggingface.total', 'Total') }}</p><p class="mt-1 text-xl font-semibold">{{ formatNumber(pool.credential_count) }}</p></div>
              <div class="rounded-lg bg-green-50 p-3 dark:bg-green-900/10"><p class="text-xs text-green-600">{{ t('admin.huggingface.available', 'Available') }}</p><p class="mt-1 text-xl font-semibold text-green-700 dark:text-green-400">{{ formatNumber(pool.available_count) }}</p></div>
              <div class="rounded-lg bg-amber-50 p-3 dark:bg-amber-900/10"><p class="text-xs text-amber-600">{{ t('admin.huggingface.cooldown', 'Cooldown') }}</p><p class="mt-1 text-xl font-semibold text-amber-700 dark:text-amber-400">{{ formatNumber(pool.cooldown_count) }}</p></div>
              <div class="rounded-lg bg-red-50 p-3 dark:bg-red-900/10"><p class="text-xs text-red-600">{{ t('admin.huggingface.disabled', 'Disabled') }}</p><p class="mt-1 text-xl font-semibold text-red-700 dark:text-red-400">{{ formatNumber(pool.disabled_count) }}</p></div>
            </div>

            <div class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-gray-500">
              <span>{{ t('admin.huggingface.priority', 'Priority') }}: <b>{{ pool.priority }}</b></span>
              <span>{{ t('admin.huggingface.weight', 'Weight') }}: <b>{{ pool.weight }}</b></span>
              <span>{{ t('admin.huggingface.breaker', 'Circuit') }}: <b>{{ pool.failure_threshold }} / {{ pool.circuit_cooldown_seconds }}s</b></span>
            </div>
            <div class="flex flex-wrap gap-1.5">
              <span v-for="model in pool.models" :key="model" class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">{{ model }}</span>
            </div>
            <div class="flex flex-wrap justify-end gap-2 border-t border-gray-100 pt-4 dark:border-dark-700">
              <button class="btn btn-secondary btn-sm" @click="openCredentials(pool)">{{ t('admin.huggingface.viewKeys', 'View keys') }}</button>
              <button class="btn btn-secondary btn-sm" @click="reconcile(pool)">{{ t('admin.huggingface.reconcile', 'Reconcile index') }}</button>
              <button class="btn btn-primary btn-sm" @click="openImport(pool)">{{ t('admin.huggingface.importKeys', 'Import keys') }}</button>
            </div>
          </div>
        </article>
      </div>
    </div>

    <BaseDialog :show="showPoolDialog" :title="editingPool ? t('admin.huggingface.editPool', 'Edit pool') : t('admin.huggingface.createPool', 'Create pool')" width="wide" @close="showPoolDialog = false">
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="savePool">
        <div><label class="input-label">{{ t('common.name', 'Name') }}</label><input v-model.trim="poolForm.name" class="input" required maxlength="100" /></div>
        <div><label class="input-label">{{ t('admin.huggingface.baseUrl', 'Base URL') }}</label><input v-model.trim="poolForm.base_url" class="input" required /></div>
        <div><label class="input-label">{{ t('admin.huggingface.priority', 'Priority') }}</label><input v-model.number="poolForm.priority" class="input" type="number" min="0" max="1000000" /></div>
        <div><label class="input-label">{{ t('admin.huggingface.weight', 'Weight') }}</label><input v-model.number="poolForm.weight" class="input" type="number" min="1" max="1000000" /></div>
        <div><label class="input-label">{{ t('common.status', 'Status') }}</label><select v-model="poolForm.status" class="input"><option value="active">active</option><option value="disabled">disabled</option></select></div>
        <div><label class="input-label">{{ t('admin.huggingface.failureThreshold', 'Failure threshold') }}</label><input v-model.number="poolForm.failure_threshold" class="input" type="number" min="1" max="1000" /></div>
        <div><label class="input-label">{{ t('admin.huggingface.circuitCooldown', 'Circuit cooldown (seconds)') }}</label><input v-model.number="poolForm.circuit_cooldown_seconds" class="input" type="number" min="1" max="86400" /></div>
        <div class="sm:col-span-2"><label class="input-label">{{ t('admin.huggingface.models', 'Models / wildcard patterns (one per line)') }}</label><textarea v-model="modelsText" class="input min-h-32 font-mono text-sm" required></textarea><p class="input-hint">meta-llama/*, Qwen/Qwen3-*, *</p></div>
      </form>
      <template #footer><button class="btn btn-secondary" @click="showPoolDialog = false">{{ t('common.cancel', 'Cancel') }}</button><button class="btn btn-primary" :disabled="saving" @click="savePool">{{ saving ? t('common.saving', 'Saving...') : t('common.save', 'Save') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="showImportDialog" :title="t('admin.huggingface.importTitle', { name: activePool?.name || '' })" width="wide" @close="closeImportDialog">
      <div class="space-y-4">
        <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-900/10 dark:text-amber-300">{{ t('admin.huggingface.importSecurity', 'Tokens are encrypted before storage and are never returned by the API. Import requires step-up authentication.') }}</div>
        <div><label class="input-label">{{ t('admin.huggingface.tokens', 'Tokens (one per line, up to 100,000)') }}</label><textarea v-model="tokensText" class="input min-h-72 font-mono text-xs" placeholder="hf_...&#10;hf_..."></textarea><p class="input-hint">{{ parsedTokenCount.toLocaleString() }} / 100,000</p></div>
        <div class="grid gap-4 sm:grid-cols-2"><div><label class="input-label">{{ t('admin.huggingface.keyPriority', 'Key priority') }}</label><input v-model.number="importPriority" class="input" type="number" min="0" max="1000000" /></div><div><label class="input-label">{{ t('admin.huggingface.concurrency', 'Concurrency per key') }}</label><input v-model.number="importConcurrency" class="input" type="number" min="1" max="100000" /></div></div>
      </div>
      <template #footer><button class="btn btn-secondary" @click="closeImportDialog">{{ t('common.cancel', 'Cancel') }}</button><button class="btn btn-primary" :disabled="importing || parsedTokenCount === 0 || parsedTokenCount > 100000" @click="submitImport">{{ importing ? t('admin.huggingface.importing', 'Importing...') : t('admin.huggingface.importKeys', 'Import keys') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="showCredentialsDialog" :title="t('admin.huggingface.keysTitle', { name: activePool?.name || '' })" width="extra-wide" @close="showCredentialsDialog = false">
      <div v-if="credentialsLoading" class="flex justify-center py-12"><LoadingSpinner /></div>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-800">
            <tr>
              <th class="px-4 py-3">ID</th>
              <th class="px-4 py-3">Token</th>
              <th class="px-4 py-3">{{ t('common.status', 'Status') }}</th>
              <th class="px-4 py-3">{{ t('admin.huggingface.errorCode', 'Upstream error code') }}</th>
              <th class="min-w-72 px-4 py-3">{{ t('admin.huggingface.errorMessage', 'Error message') }}</th>
              <th class="px-4 py-3">{{ t('admin.huggingface.priority', 'Priority') }}</th>
              <th class="px-4 py-3">{{ t('admin.huggingface.recoverAt', 'Recover / cooldown') }}</th>
              <th class="px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in credentials.items" :key="item.account_id" class="align-top">
              <td class="px-4 py-3">{{ item.account_id }}</td>
              <td class="px-4 py-3 font-mono">••••••{{ item.token_suffix }}</td>
              <td class="px-4 py-3">
                <span :class="credentialStatusClass(item)">{{ credentialStatusLabel(item) }}</span>
                <p v-if="item.disabled_reason" class="mt-1 max-w-48 text-xs text-gray-500" :title="item.disabled_reason">
                  {{ credentialReasonLabel(item.disabled_reason) }}
                </p>
              </td>
              <td class="px-4 py-3">
                <span v-if="item.upstream_status_code" class="badge badge-danger whitespace-nowrap">HTTP {{ item.upstream_status_code }}</span>
                <span v-else class="text-gray-400">-</span>
              </td>
              <td class="max-w-xl whitespace-normal break-words px-4 py-3 text-xs text-gray-700 dark:text-gray-300" :title="item.error_message || ''">
                {{ item.error_message || credentialReasonLabel(item.disabled_reason) || '-' }}
              </td>
              <td class="whitespace-nowrap px-4 py-3">{{ item.priority }} / {{ item.concurrency }}×</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500">{{ formatDate(item.recover_at || item.rate_limit_reset_at || item.temp_unschedulable_until) }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-right">
                <button v-if="item.status !== 'active' || !item.schedulable" class="btn btn-ghost btn-sm" @click="recoverKey(item.account_id)">{{ t('admin.huggingface.recover', 'Recover') }}</button>
                <button class="btn btn-ghost btn-sm text-red-600" @click="removeKey(item.account_id)"><Icon name="trash" size="sm" /></button>
              </td>
            </tr>
            <tr v-if="credentials.items.length === 0"><td colspan="8" class="px-4 py-10 text-center text-gray-500">{{ t('admin.huggingface.noKeys', 'No keys') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="mt-4 flex items-center justify-between text-sm"><span>{{ credentials.total.toLocaleString() }} {{ t('admin.huggingface.total', 'total') }}</span><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="credentialOffset === 0" @click="changeCredentialPage(-1)">{{ t('pagination.previous', 'Previous') }}</button><button class="btn btn-secondary btn-sm" :disabled="credentialOffset + credentialLimit >= credentials.total" @click="changeCredentialPage(1)">{{ t('pagination.next', 'Next') }}</button></div></div>
    </BaseDialog>

    <TotpStepUpDialog :controller="hfImportStepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { adminAPI } from '@/api/admin'
import type { HFCredentialItem, HFCredentialPage, HuggingFacePool, HuggingFacePoolInput } from '@/api/admin/huggingface'
import type { AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const hfImportStepUp = useStepUp()
const groups = ref<AdminGroup[]>([])
const groupsLoading = ref(false)
const selectedGroupId = ref(0)
const pools = ref<HuggingFacePool[]>([])
const loading = ref(false)
const saving = ref(false)
const importing = ref(false)
const showPoolDialog = ref(false)
const showImportDialog = ref(false)
const showCredentialsDialog = ref(false)
const editingPool = ref<HuggingFacePool | null>(null)
const activePool = ref<HuggingFacePool | null>(null)
const modelsText = ref('*')
const tokensText = ref('')
const importPriority = ref(50)
const importConcurrency = ref(1)
const credentialsLoading = ref(false)
const credentialLimit = 50
const credentialOffset = ref(0)
const credentials = ref<HFCredentialPage>({ items: [], total: 0, limit: credentialLimit, offset: 0 })
const emptyPoolForm = (): HuggingFacePoolInput => ({ group_id: selectedGroupId.value, name: '', base_url: 'https://router.huggingface.co', priority: 50, weight: 100, status: 'active', models: ['*'], failure_threshold: 5, circuit_cooldown_seconds: 30 })
const poolForm = ref<HuggingFacePoolInput>(emptyPoolForm())
const parsedTokens = computed(() => tokensText.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean))
const parsedTokenCount = computed(() => parsedTokens.value.length)

function formatNumber(value?: number) { return Number(value || 0).toLocaleString() }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString() : '-' }
function showError(error: unknown) { appStore.showError(extractApiErrorMessage(error)) }

function credentialIsCooling(item: HFCredentialItem) {
  const until = item.recover_at || item.rate_limit_reset_at || item.temp_unschedulable_until
  return Boolean(until && new Date(until).getTime() > Date.now())
}

function credentialStatusClass(item: HFCredentialItem) {
  if (credentialIsCooling(item)) return 'badge badge-warning'
  if (item.status === 'active' && item.schedulable) return 'badge badge-success'
  return 'badge badge-danger'
}

function credentialStatusLabel(item: HFCredentialItem) {
  if (credentialIsCooling(item)) return t('admin.huggingface.statusCooldown')
  if (item.status === 'error') return t('admin.huggingface.statusError')
  if (item.status === 'disabled') return t('admin.huggingface.statusDisabled')
  if (!item.schedulable) return t('admin.huggingface.statusPaused')
  return t('admin.huggingface.statusAvailable')
}

function credentialReasonLabel(reason?: string) {
  switch (reason) {
    case 'monthly_included_credits_exhausted': return t('admin.huggingface.reasonMonthlyExhausted')
    case 'invalid_token': return t('admin.huggingface.reasonInvalidToken')
    case 'forbidden': return t('admin.huggingface.reasonForbidden')
    case 'credential_decrypt_failed': return t('admin.huggingface.reasonDecryptFailed')
    case 'rate_limited': return t('admin.huggingface.reasonRateLimited')
    case 'billing_required': return t('admin.huggingface.reasonBillingRequired')
    case 'transient_upstream_failure': return t('admin.huggingface.reasonTransientFailure')
    default: return reason || ''
  }
}

async function loadGroups() {
  groupsLoading.value = true
  try {
    groups.value = await adminAPI.groups.getAll('huggingface')
    if (!groups.value.some(group => group.id === selectedGroupId.value)) selectedGroupId.value = groups.value[0]?.id || 0
  } catch (error) { showError(error) } finally { groupsLoading.value = false }
}
async function loadPools() {
  if (!selectedGroupId.value) { pools.value = []; return }
  loading.value = true
  try { pools.value = await adminAPI.huggingface.listPools(selectedGroupId.value) } catch (error) { showError(error) } finally { loading.value = false }
}
function openCreatePool() { editingPool.value = null; poolForm.value = emptyPoolForm(); modelsText.value = '*'; showPoolDialog.value = true }
function openEditPool(pool: HuggingFacePool) { editingPool.value = pool; poolForm.value = { group_id: pool.group_id, name: pool.name, base_url: pool.base_url, priority: pool.priority, weight: pool.weight, status: pool.status, models: [...pool.models], failure_threshold: pool.failure_threshold, circuit_cooldown_seconds: pool.circuit_cooldown_seconds }; modelsText.value = pool.models.join('\n'); showPoolDialog.value = true }
async function savePool() {
  const models = modelsText.value.split(/[\r\n,]+/).map(v => v.trim()).filter(Boolean)
  if (!poolForm.value.name || models.length === 0) return
  saving.value = true
  try {
    const input = { ...poolForm.value, group_id: selectedGroupId.value, models }
    if (editingPool.value) await adminAPI.huggingface.updatePool(editingPool.value.id, input)
    else await adminAPI.huggingface.createPool(input)
    showPoolDialog.value = false; appStore.showSuccess(t('common.saved', 'Saved')); await loadPools()
  } catch (error) { showError(error) } finally { saving.value = false }
}
async function removePool(pool: HuggingFacePool) { if (!window.confirm(t('admin.huggingface.deleteConfirm', { name: pool.name }))) return; try { await adminAPI.huggingface.deletePool(pool.id); appStore.showSuccess(t('common.deleted', 'Deleted')); await loadPools() } catch (error) { showError(error) } }
function openImport(pool: HuggingFacePool) { activePool.value = pool; tokensText.value = ''; importPriority.value = 50; importConcurrency.value = 1; showImportDialog.value = true }
function closeImportDialog() { showImportDialog.value = false; tokensText.value = '' }
async function submitImport() {
  if (!activePool.value || parsedTokenCount.value === 0 || parsedTokenCount.value > 100000) return
  importing.value = true
  try {
    const result = await hfImportStepUp.run(() => adminAPI.huggingface.importCredentials(activePool.value!.id, parsedTokens.value.map(token => ({ token, priority: importPriority.value, concurrency: importConcurrency.value }))))
    const resultMessage = t('admin.huggingface.importResult', { imported: result.imported, duplicate: result.duplicate, invalid: result.invalid })
    if (result.index_pending) appStore.showWarning(`${resultMessage} ${t('admin.huggingface.indexPending')}`)
    else appStore.showSuccess(resultMessage)
    closeImportDialog(); await loadPools()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? t('stepUp.adminApiKeyForbidden') : t('stepUp.notEnabled'))
      return
    }
    showError(error)
  } finally { importing.value = false }
}
async function reconcile(pool: HuggingFacePool) { try { await adminAPI.huggingface.reconcilePool(pool.id); appStore.showSuccess(t('admin.huggingface.reconcileDone', 'Index reconciled')) } catch (error) { showError(error) } }
async function openCredentials(pool: HuggingFacePool) { activePool.value = pool; credentialOffset.value = 0; showCredentialsDialog.value = true; await loadCredentials() }
async function loadCredentials() { if (!activePool.value) return; credentialsLoading.value = true; try { credentials.value = await adminAPI.huggingface.listCredentials(activePool.value.id, credentialLimit, credentialOffset.value) } catch (error) { showError(error) } finally { credentialsLoading.value = false } }
async function changeCredentialPage(direction: number) { credentialOffset.value = Math.max(0, credentialOffset.value + direction * credentialLimit); await loadCredentials() }
async function recoverKey(accountId: number) { if (!activePool.value) return; try { await adminAPI.huggingface.recoverCredential(activePool.value.id, accountId); await Promise.all([loadCredentials(), loadPools()]) } catch (error) { showError(error) } }
async function removeKey(accountId: number) { if (!activePool.value || !window.confirm(t('admin.huggingface.deleteKeyConfirm', 'Delete this key?'))) return; try { await adminAPI.huggingface.deleteCredential(activePool.value.id, accountId); await Promise.all([loadCredentials(), loadPools()]) } catch (error) { showError(error) } }

watch(selectedGroupId, loadPools)
onMounted(loadGroups)
</script>
