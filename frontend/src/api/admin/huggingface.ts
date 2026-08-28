import { apiClient } from '../client'

export type HFPoolStatus = 'active' | 'disabled'

export interface HuggingFacePool {
  id: number
  group_id: number
  name: string
  base_url: string
  priority: number
  weight: number
  status: HFPoolStatus
  models: string[]
  failure_threshold: number
  circuit_cooldown_seconds: number
  credential_count: number
  available_count: number
  cooldown_count: number
  disabled_count: number
  created_at: string
  updated_at: string
}

export interface HuggingFacePoolInput {
  group_id: number
  name: string
  base_url: string
  priority: number
  weight: number
  status: HFPoolStatus
  models: string[]
  failure_threshold: number
  circuit_cooldown_seconds: number
}

export interface HFCredentialImport {
  token: string
  priority: number
  concurrency: number
}

export interface HFImportResult {
  received: number
  imported: number
  duplicate: number
  invalid: number
  index_pending: boolean
}

export interface HFCredentialItem {
  account_id: number
  pool_id: number
  name: string
  token_suffix: string
  priority: number
  concurrency: number
  status: string
  schedulable: boolean
  disabled_reason?: string
  upstream_status_code?: number
  error_message?: string
  rate_limit_reset_at?: string
  temp_unschedulable_until?: string
  recover_at?: string
  last_used_at?: string
  created_at: string
}

export interface HFCredentialPage {
  items: HFCredentialItem[]
  total: number
  limit: number
  offset: number
}

export async function listPools(groupId: number): Promise<HuggingFacePool[]> {
  const { data } = await apiClient.get<HuggingFacePool[]>('/admin/huggingface/pools', {
    params: { group_id: groupId }
  })
  return data
}

export async function createPool(input: HuggingFacePoolInput): Promise<HuggingFacePool> {
  const { data } = await apiClient.post<HuggingFacePool>('/admin/huggingface/pools', input)
  return data
}

export async function updatePool(id: number, input: HuggingFacePoolInput): Promise<HuggingFacePool> {
  const { data } = await apiClient.put<HuggingFacePool>(`/admin/huggingface/pools/${id}`, input)
  return data
}

export async function deletePool(id: number): Promise<void> {
  await apiClient.delete(`/admin/huggingface/pools/${id}`)
}

export async function importCredentials(id: number, credentials: HFCredentialImport[]): Promise<HFImportResult> {
  const { data } = await apiClient.post<HFImportResult>(
    `/admin/huggingface/pools/${id}/credentials`,
    { credentials },
    { timeout: 120_000 }
  )
  return data
}

export async function listCredentials(id: number, limit = 50, offset = 0): Promise<HFCredentialPage> {
  const { data } = await apiClient.get<HFCredentialPage>(`/admin/huggingface/pools/${id}/credentials`, {
    params: { limit, offset }
  })
  return data
}

export async function recoverCredential(poolId: number, accountId: number): Promise<void> {
  await apiClient.post(`/admin/huggingface/pools/${poolId}/credentials/${accountId}/recover`)
}

export async function deleteCredential(poolId: number, accountId: number): Promise<void> {
  await apiClient.delete(`/admin/huggingface/pools/${poolId}/credentials/${accountId}`)
}

export async function reconcilePool(id: number): Promise<void> {
  await apiClient.post(`/admin/huggingface/pools/${id}/reconcile`, undefined, { timeout: 120_000 })
}

export default {
  listPools,
  createPool,
  updatePool,
  deletePool,
  importCredentials,
  listCredentials,
  recoverCredential,
  deleteCredential,
  reconcilePool
}
