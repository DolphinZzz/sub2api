import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearLegacyStudioStorage } from '@/utils/studioStorage'

afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function pendingDeleteRequest() {
  return {
    onsuccess: null,
    onerror: null,
    onblocked: null,
  } as unknown as IDBOpenDBRequest
}

describe('clearLegacyStudioStorage', () => {
  it('removes the browser history fallback and active-session marker', async () => {
    localStorage.setItem('sub2api_studio_sessions', '[{"id":"legacy"}]')
    localStorage.setItem('sub2api_studio_active_session', 'legacy')
    const request = pendingDeleteRequest()
    const deleteDatabase = vi.fn(() => request)
    vi.stubGlobal('indexedDB', { deleteDatabase })

    const clearing = clearLegacyStudioStorage()
    expect(deleteDatabase).toHaveBeenCalledWith('sub2api-studio')
    request.onsuccess?.(new Event('success') as Event & { target: IDBOpenDBRequest })
    await clearing

    expect(localStorage.getItem('sub2api_studio_sessions')).toBeNull()
    expect(localStorage.getItem('sub2api_studio_active_session')).toBeNull()
    expect(localStorage.getItem('sub2api_studio_server_history_v1')).toBe('1')
  })

  it('retries database deletion after a blocked attempt', async () => {
    const request = pendingDeleteRequest()
    const deleteDatabase = vi.fn(() => request)
    vi.stubGlobal('indexedDB', { deleteDatabase })

    const clearing = clearLegacyStudioStorage()
    request.onblocked?.(new Event('blocked') as Event & { target: IDBOpenDBRequest })
    await clearing
    expect(localStorage.getItem('sub2api_studio_server_history_v1')).toBeNull()

    const retry = pendingDeleteRequest()
    deleteDatabase.mockReturnValue(retry)
    const retrying = clearLegacyStudioStorage()
    retry.onsuccess?.(new Event('success') as Event & { target: IDBOpenDBRequest })
    await retrying
    expect(deleteDatabase).toHaveBeenCalledTimes(2)
  })
})
