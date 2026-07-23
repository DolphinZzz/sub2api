export type StudioMode = 'chat' | 'image'

export interface StudioImageData {
  assetId: string
  format: string
  revisedPrompt?: string
  mimeType?: string
  byteSize?: number
  blobUrl?: string
}

export interface StudioRequestSummary {
  id: string
  turnId: string
  endpoint: string
  apiKeyId: number | null
  apiKeyName: string
  model: string
  status: string
  asyncTaskId?: string
  durationMs: number | null
  errorCode?: string
  errorMessage?: string
  image?: {
    action?: string
    size?: string
    aspectRatio?: string
    quality?: string
    background?: string
    outputFormat?: string
    count?: number
  }
  createdAt: number
}

export interface StudioMessage {
  id: string
  turnId?: string
  role: 'user' | 'assistant'
  type: 'text' | 'images' | 'error'
  content: string
  images?: StudioImageData[]
  requests?: StudioRequestSummary[]
  createdAt: number
}

export interface StudioSession {
  id: string
  title: string
  mode: StudioMode
  status?: string
  messages: StudioMessage[]
  createdAt: number
  updatedAt: number
  expiresAt?: number
}

const LEGACY_DB_NAME = 'sub2api-studio'
const LEGACY_STORAGE_KEYS = [
  'sub2api_studio_sessions',
  'sub2api_studio_active_session',
]
const MIGRATION_MARKER = 'sub2api_studio_server_history_v1'

function deleteLegacyDatabase(): Promise<boolean> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(true)

  return new Promise((resolve) => {
    const request = indexedDB.deleteDatabase(LEGACY_DB_NAME)
    request.onsuccess = () => resolve(true)
    request.onerror = () => resolve(false)
    request.onblocked = () => resolve(false)
  })
}

/** Clears browser-only Studio history once after server history is enabled. */
export async function clearLegacyStudioStorage(): Promise<void> {
  if (typeof localStorage === 'undefined' || localStorage.getItem(MIGRATION_MARKER) === '1') return

  for (const key of LEGACY_STORAGE_KEYS) localStorage.removeItem(key)
  if (await deleteLegacyDatabase()) localStorage.setItem(MIGRATION_MARKER, '1')
}
