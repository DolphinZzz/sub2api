import { apiClient } from '@/api/client'
import { buildApiUrl } from '@/api/url'
import type {
  StudioImageData,
  StudioMessage,
  StudioMode,
  StudioRequestSummary,
  StudioSession,
} from '@/utils/studioStorage'

export type StudioRole = 'user' | 'assistant'

export interface StudioInputMessage {
  role: StudioRole
  text: string
}

export interface StudioImageOptions {
  action: 'generate' | 'edit'
  size: string
  aspectRatio: string
  quality: 'low' | 'medium' | 'high'
  background: 'auto' | 'transparent'
  outputFormat: 'png' | 'jpeg' | 'webp'
  referenceImages: string[]
}

export const STUDIO_IMAGE_SIZES = [
  '1024x1024',
  '1536x1024',
  '1024x1536',
  '2048x2048',
  '2048x1152',
  '3840x2160',
  '2160x3840',
] as const

const IMAGE_SIZES_BY_ASPECT_RATIO: Record<string, readonly string[]> = {
  '1:1': ['1024x1024', '2048x2048'],
  '2:3': ['1024x1536'],
  '3:2': ['1536x1024'],
  '9:16': ['2160x3840'],
  '16:9': ['2048x1152', '3840x2160'],
}

export const STUDIO_IMAGE_ASPECT_RATIOS = Object.keys(IMAGE_SIZES_BY_ASPECT_RATIO)

const DEFAULT_IMAGE_SIZE = STUDIO_IMAGE_SIZES[0]
const STUDIO_IMAGE_SIZE_SET = new Set<string>(STUDIO_IMAGE_SIZES)

export interface StudioResponseEnvelope {
  turn_id: string
  api_key_id: number
  endpoint: string
  payload: unknown
}

export interface StudioPersistedEvent {
  type: 'studio.persisted'
  request_id?: string
  session_id?: string
  turn_id?: string
  message_id?: string
  asset_ids?: string[]
}

export interface StudioImageTask {
  id: string
  taskId: string
  requestId: string
  status: 'processing' | 'completed' | 'failed' | string
  httpStatus?: number
  persisted: boolean
  errorMessage?: string
  createdAt?: number
  completedAt?: number
  expiresAt?: number
}

export interface StudioStreamHandlers {
  onTextDelta?: (delta: string) => void
  onPersisted?: (event: StudioPersistedEvent) => void
}

interface ResponsesEvent {
  type?: string
  delta?: string
  message?: string
  error?: { message?: string }
  response?: {
    error?: { message?: string }
    output?: Array<Record<string, unknown>>
  }
  item?: Record<string, unknown>
  [key: string]: unknown
}

type UnknownRecord = Record<string, unknown>

function record(value: unknown): UnknownRecord {
  return value && typeof value === 'object' ? value as UnknownRecord : {}
}

function stringValue(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function numberValue(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() && Number.isFinite(Number(value))) return Number(value)
  return null
}

function timestamp(value: unknown): number {
  const numeric = numberValue(value)
  if (numeric !== null) return numeric > 1e12 ? numeric : numeric * 1000
  const parsed = Date.parse(stringValue(value))
  return Number.isNaN(parsed) ? Date.now() : parsed
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function unwrapList(value: unknown): unknown[] {
  if (Array.isArray(value)) return value
  const source = record(value)
  return arrayValue(source.items ?? source.sessions ?? source.data)
}

function normalizeImage(value: unknown): StudioImageData | null {
  const source = record(value)
  const asset = record(source.asset)
  const assetId = stringValue(source.asset_id ?? source.assetId ?? asset.id ?? source.id)
  if (!assetId) return null
  const mimeType = stringValue(source.mime_type ?? source.mimeType ?? asset.mime_type ?? asset.mimeType)
  const format = stringValue(source.output_format ?? source.format ?? asset.output_format ?? asset.format)
    || mimeType.split('/')[1]
    || 'png'
  return {
    assetId,
    format,
    revisedPrompt: stringValue(source.revised_prompt ?? source.revisedPrompt) || undefined,
    mimeType: mimeType || undefined,
    byteSize: numberValue(source.byte_size ?? source.byteSize ?? asset.byte_size ?? asset.byteSize) ?? undefined,
  }
}

function imageOptionsFromPayload(value: unknown): StudioRequestSummary['image'] | undefined {
  const payload = record(value)
  const tool = record(arrayValue(payload.tools)[0])
  if (tool.type !== 'image_generation' && !stringValue(payload.prompt)) return undefined
  const direct = tool.type === 'image_generation' ? tool : payload
  return {
    action: stringValue(direct.action) || (arrayValue(payload.images).length ? 'edit' : 'generate'),
    size: stringValue(direct.size) || undefined,
    aspectRatio: stringValue(direct.aspect_ratio) || (stringValue(direct.size) ? imageAspectRatioForSize(stringValue(direct.size)) : undefined),
    quality: stringValue(direct.quality) || undefined,
    background: stringValue(direct.background) || undefined,
    outputFormat: stringValue(direct.output_format) || undefined,
    count: numberValue(direct.n) ?? undefined,
  }
}

export function normalizeStudioRequest(value: unknown): StudioRequestSummary {
  const source = record(value)
  const requestBody = source.request ?? source.request_body ?? source.payload
  return {
    id: stringValue(source.id ?? source.request_id),
    turnId: stringValue(source.turn_id ?? source.turnId),
    endpoint: stringValue(source.endpoint),
    apiKeyId: numberValue(source.api_key_id ?? source.apiKeyId),
    apiKeyName: stringValue(source.api_key_name ?? source.apiKeyName),
    model: stringValue(source.model) || stringValue(record(requestBody).model),
    status: stringValue(source.status, 'unknown'),
    asyncTaskId: stringValue(source.async_task_id ?? source.asyncTaskId) || undefined,
    durationMs: numberValue(source.duration_ms ?? source.durationMs),
    errorCode: stringValue(source.error_code ?? source.errorCode) || undefined,
    errorMessage: stringValue(source.error_message ?? source.errorMessage) || undefined,
    image: imageOptionsFromPayload(requestBody),
    createdAt: timestamp(source.created_at ?? source.createdAt),
  }
}

function normalizeMessage(value: unknown, requests: StudioRequestSummary[]): StudioMessage {
  const source = record(value)
  const turnId = stringValue(source.turn_id ?? source.turnId)
  const images = [
    ...arrayValue(source.images ?? source.assets ?? source.generations),
    ...arrayValue(source.asset_ids).map((assetId) => ({ asset_id: assetId })),
  ].map(normalizeImage)
    .filter((image): image is StudioImageData => Boolean(image))
  const messageRequests = requests.filter((request) => turnId && request.turnId === turnId)
  for (const requestId of arrayValue(source.request_ids)) {
    const id = stringValue(requestId)
    if (!id || messageRequests.some((request) => request.id === id)) continue
    messageRequests.push({
      id,
      turnId,
      endpoint: '',
      apiKeyId: null,
      apiKeyName: '',
      model: '',
      status: stringValue(source.status, 'completed'),
      durationMs: null,
      createdAt: timestamp(source.created_at ?? source.createdAt),
    })
  }
  return {
    id: stringValue(source.id ?? source.message_id),
    turnId: turnId || undefined,
    role: source.role === 'user' ? 'user' : 'assistant',
    type: source.type === 'images' || source.message_type === 'images'
      ? 'images'
      : source.type === 'error' || source.message_type === 'error'
        ? 'error'
        : 'text',
    content: stringValue(source.content ?? source.text),
    images: images.length ? images : undefined,
    requests: messageRequests.length ? messageRequests : undefined,
    createdAt: timestamp(source.created_at ?? source.createdAt),
  }
}

export function normalizeStudioSession(value: unknown): StudioSession {
  const outer = record(value)
  const source = record(outer.session && typeof outer.session === 'object' ? outer.session : value)
  const requests = arrayValue(outer.requests ?? source.requests).map(normalizeStudioRequest)
  const messages = arrayValue(outer.messages ?? source.messages).map((message) => normalizeMessage(message, requests))
  return {
    id: stringValue(source.id ?? source.session_id),
    title: stringValue(source.title),
    mode: source.mode === 'image' ? 'image' : 'chat',
    status: stringValue(source.status) || undefined,
    messages,
    createdAt: timestamp(source.created_at ?? source.createdAt),
    updatedAt: timestamp(source.updated_at ?? source.updatedAt),
    expiresAt: source.expires_at || source.expiresAt ? timestamp(source.expires_at ?? source.expiresAt) : undefined,
  }
}

export async function listStudioSessions(): Promise<StudioSession[]> {
  const { data } = await apiClient.get<unknown>('/studio/sessions')
  return unwrapList(data).map(normalizeStudioSession).sort((a, b) => b.updatedAt - a.updatedAt)
}

export async function createStudioSession(mode: StudioMode, title: string): Promise<StudioSession> {
  const { data } = await apiClient.post<unknown>('/studio/sessions', { mode, title })
  return normalizeStudioSession(data)
}

export async function getStudioSession(id: string): Promise<StudioSession> {
  const { data } = await apiClient.get<unknown>(`/studio/sessions/${encodeURIComponent(id)}`)
  return normalizeStudioSession(data)
}

export async function deleteStudioSession(id: string): Promise<void> {
  await apiClient.delete(`/studio/sessions/${encodeURIComponent(id)}`)
}

export async function getStudioRequest(id: string): Promise<StudioRequestSummary> {
  const { data } = await apiClient.get<unknown>(`/studio/requests/${encodeURIComponent(id)}`)
  return normalizeStudioRequest(data)
}

function authHeaders(): HeadersInit {
  const token = localStorage.getItem('auth_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function responseError(response: Response): Promise<Error> {
  const raw = await response.text()
  let message = raw.trim()
  try {
    const parsed = record(JSON.parse(raw))
    message = stringValue(record(parsed.error).message ?? parsed.message, message)
  } catch {
    // Preserve non-JSON server error text.
  }
  return new Error(message || `请求失败（HTTP ${response.status}）`)
}

export async function fetchStudioAsset(assetId: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(buildApiUrl(`/studio/assets/${encodeURIComponent(assetId)}/content`), {
    headers: authHeaders(),
    signal,
  })
  if (!response.ok) throw await responseError(response)
  return response.blob()
}

export function buildChatPayload(model: string, messages: StudioInputMessage[]) {
  return {
    model,
    input: messages.map((message) => ({
      type: 'message',
      role: message.role,
      content: [{
        type: message.role === 'assistant' ? 'output_text' : 'input_text',
        text: message.text,
      }],
    })),
    stream: true,
    store: false,
  }
}

function imagePixels(size: string): number {
  const [width, height] = size.split('x').map(Number)
  return width * height
}

export function imageAspectRatioForSize(size: string): string {
  for (const [aspectRatio, sizes] of Object.entries(IMAGE_SIZES_BY_ASPECT_RATIO)) {
    if (sizes.includes(size)) return aspectRatio
  }
  return '1:1'
}

export function imageSizeForAspectRatio(size: string, aspectRatio: string): string {
  const normalizedSize = STUDIO_IMAGE_SIZE_SET.has(size) ? size : DEFAULT_IMAGE_SIZE
  const candidates = IMAGE_SIZES_BY_ASPECT_RATIO[aspectRatio]
  if (!candidates?.length) return normalizedSize
  if (candidates.includes(normalizedSize)) return normalizedSize

  const targetPixels = imagePixels(normalizedSize)
  return candidates.reduce((closest, candidate) => (
    Math.abs(imagePixels(candidate) - targetPixels) < Math.abs(imagePixels(closest) - targetPixels)
      ? candidate
      : closest
  ))
}

export function buildImagePayload(model: string, prompt: string, options: StudioImageOptions) {
  const content: Array<Record<string, string>> = [{ type: 'input_text', text: prompt }]
  for (const imageUrl of options.referenceImages.slice(0, 5)) content.push({ type: 'input_image', image_url: imageUrl })
  const tool = {
    type: 'image_generation',
    action: options.action,
    size: imageSizeForAspectRatio(options.size, options.aspectRatio),
    quality: options.quality,
    background: options.background,
    output_format: options.outputFormat,
  }
  return {
    model,
    input: [{ type: 'message', role: 'user', content }],
    tools: [tool],
    tool_choice: { type: 'image_generation' },
    stream: true,
    store: false,
  }
}

export function buildAsyncImagePayload(prompt: string, options: StudioImageOptions) {
  const payload: Record<string, unknown> = {
    model: 'gpt-image-2',
    prompt,
    size: imageSizeForAspectRatio(options.size, options.aspectRatio),
    quality: options.quality,
    background: options.background,
    output_format: options.outputFormat,
  }
  if (options.action === 'edit') {
    payload.images = options.referenceImages.slice(0, 5).map((imageUrl) => ({ image_url: imageUrl }))
  }
  return payload
}

function normalizeStudioImageTask(value: unknown): StudioImageTask {
  const source = record(value)
  const error = record(source.error)
  return {
    id: stringValue(source.id ?? source.task_id),
    taskId: stringValue(source.task_id ?? source.id),
    requestId: stringValue(source.request_id),
    status: stringValue(source.status, 'failed'),
    httpStatus: numberValue(source.http_status) ?? undefined,
    persisted: source.persisted === true,
    errorMessage: stringValue(error.message) || undefined,
    createdAt: numberValue(source.created_at) ?? undefined,
    completedAt: numberValue(source.completed_at) ?? undefined,
    expiresAt: numberValue(source.expires_at) ?? undefined,
  }
}

export async function submitStudioImage(sessionId: string, envelope: StudioResponseEnvelope, signal?: AbortSignal): Promise<StudioImageTask> {
  const { data } = await apiClient.post<unknown>(
    `/studio/sessions/${encodeURIComponent(sessionId)}/images/async`,
    envelope,
    { signal, timeout: 0 },
  )
  return normalizeStudioImageTask(data)
}

export async function getStudioImageTask(requestId: string, signal?: AbortSignal): Promise<StudioImageTask> {
  const { data } = await apiClient.get<unknown>(
    `/studio/requests/${encodeURIComponent(requestId)}/image-task`,
    { signal, timeout: 0 },
  )
  return normalizeStudioImageTask(data)
}

export async function waitForStudioImageTask(requestId: string, signal?: AbortSignal): Promise<StudioImageTask> {
  while (true) {
    const task = await getStudioImageTask(requestId, signal)
    if (task.status !== 'processing' && task.status !== 'completed') {
      throw new Error(task.errorMessage || '图片生成失败')
    }
    if (task.status === 'completed' && task.persisted) return task
    await new Promise<void>((resolve, reject) => {
      const onAbort = () => {
        window.clearTimeout(timer)
        reject(new DOMException('Aborted', 'AbortError'))
      }
      const timer = window.setTimeout(() => {
        signal?.removeEventListener('abort', onAbort)
        resolve()
      }, 3000)
      signal?.addEventListener('abort', onAbort, { once: true })
    })
  }
}

function eventError(event: ResponsesEvent): string {
  return event.error?.message || event.response?.error?.message || stringValue(event.message)
}

export function consumeResponsesEvent(event: ResponsesEvent, handlers: StudioStreamHandlers) {
  if (event.type === 'response.output_text.delta' && event.delta) handlers.onTextDelta?.(event.delta)
  if (event.type === 'studio.persisted') handlers.onPersisted?.(event as StudioPersistedEvent)
  if (event.type === 'studio.persistence_failed' || event.type === 'error' || event.type === 'response.failed') {
    throw new Error(eventError(event) || '请求失败')
  }
}

export async function requestStudioResponse(
  sessionId: string,
  envelope: StudioResponseEnvelope,
  handlers: StudioStreamHandlers,
  signal?: AbortSignal,
): Promise<StudioPersistedEvent | undefined> {
  const response = await fetch(buildApiUrl(`/studio/sessions/${encodeURIComponent(sessionId)}/responses`), {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
    },
    body: JSON.stringify(envelope),
    signal,
  })
  if (!response.ok) throw await responseError(response)
  if (!response.body) throw new Error('响应流不可用')

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let receivedTextDelta = false
  let persisted: StudioPersistedEvent | undefined
  let buffer = ''

  const consumeBlock = (block: string) => {
    const data = block
      .split(/\r?\n/)
      .filter((line) => line.startsWith('data:'))
      .map((line) => line.slice(5).trimStart())
      .join('\n')
      .trim()
    if (!data || data === '[DONE]') return
    const event = JSON.parse(data) as ResponsesEvent
    consumeResponsesEvent(event, {
      ...handlers,
      onTextDelta: (delta) => {
        receivedTextDelta = true
        handlers.onTextDelta?.(delta)
      },
      onPersisted: (value) => {
        persisted = value
        handlers.onPersisted?.(value)
      },
    })
    if (event.type === 'response.completed' && !receivedTextDelta) {
      for (const item of event.response?.output || []) {
        if (item.type !== 'message' || !Array.isArray(item.content)) continue
        for (const part of item.content as Array<Record<string, unknown>>) {
          if (part.type === 'output_text' && typeof part.text === 'string') handlers.onTextDelta?.(part.text)
        }
      }
    }
  }

  while (true) {
    const { value, done } = await reader.read()
    buffer += decoder.decode(value, { stream: !done })
    const blocks = buffer.split(/\r?\n\r?\n/)
    buffer = blocks.pop() || ''
    for (const block of blocks) consumeBlock(block)
    if (done) break
  }
  if (buffer.trim()) consumeBlock(buffer)
  return persisted
}
