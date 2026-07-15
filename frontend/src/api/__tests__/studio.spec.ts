import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildChatPayload,
  buildImagePayload,
  consumeResponsesEvent,
  normalizeStudioRequest,
  normalizeStudioSession,
  requestStudioResponse,
} from '@/api/studio'

beforeEach(() => localStorage.setItem('auth_token', 'jwt-test'))
afterEach(() => {
  localStorage.clear()
  vi.unstubAllGlobals()
})

describe('studio API helpers', () => {
  it('builds a multi-turn Responses chat payload', () => {
    expect(buildChatPayload('gpt-5.5', [
      { role: 'user', text: 'hello' },
      { role: 'assistant', text: 'hi' },
    ])).toMatchObject({
      model: 'gpt-5.5',
      input: [
        { role: 'user', content: [{ type: 'input_text', text: 'hello' }] },
        { role: 'assistant', content: [{ type: 'output_text', text: 'hi' }] },
      ],
      stream: true,
      store: false,
    })
  })

  it('builds a Responses image edit payload with only supported tool controls', () => {
    const payload = buildImagePayload('gpt-5.5', 'redesign it', {
      action: 'edit',
      size: '2048x1152',
      aspectRatio: '16:9',
      quality: 'high',
      background: 'transparent',
      outputFormat: 'webp',
      referenceImages: ['data:image/png;base64,abc'],
    })
    expect(payload).toMatchObject({
      model: 'gpt-5.5',
      input: [{ content: [
        { type: 'input_text', text: 'redesign it' },
        { type: 'input_image', image_url: 'data:image/png;base64,abc' },
      ] }],
      tools: [{
        type: 'image_generation',
        action: 'edit',
        size: '2048x1152',
        quality: 'high',
        background: 'transparent',
        output_format: 'webp',
      }],
      stream: true,
      store: false,
    })
    expect(payload.tools[0]).not.toHaveProperty('model')
    expect(payload.tools[0]).not.toHaveProperty('n')
    expect(payload.tools[0]).not.toHaveProperty('aspect_ratio')
    expect(payload.tool_choice).toEqual({ type: 'image_generation' })
  })

  it('builds one Responses image generation payload per requested image', () => {
    const payload = buildImagePayload('gpt-5.5', 'draw a cat', {
      action: 'generate',
      size: '1024x1024',
      aspectRatio: '1:1',
      quality: 'low',
      background: 'auto',
      outputFormat: 'png',
      referenceImages: [],
    })

    expect(payload.tools[0]).toEqual({
      type: 'image_generation',
      action: 'generate',
      size: '1024x1024',
      quality: 'low',
      background: 'auto',
      output_format: 'png',
    })
    expect(payload.tools[0]).not.toHaveProperty('n')
    expect(payload.tools[0]).not.toHaveProperty('aspect_ratio')
    expect(payload.model).toBe('gpt-5.5')
    expect(payload.tool_choice).toEqual({ type: 'image_generation' })
  })

  it('encodes the selected aspect ratio in the supported size field', () => {
    const payload = buildImagePayload('gpt-5.5', 'draw a landscape', {
      action: 'generate',
      size: '1024x1024',
      aspectRatio: '16:9',
      quality: 'medium',
      background: 'auto',
      outputFormat: 'png',
      referenceImages: [],
    })

    expect(payload.tools[0].size).toBe('1280x720')
    expect(payload.tools[0]).not.toHaveProperty('aspect_ratio')
  })

  it('restores message asset and request references from session detail', () => {
    const session = normalizeStudioSession({
      id: 'session-1',
      title: 'hello',
      mode: 'image',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-02T00:00:00Z',
      messages: [{
        id: 'message-1',
        turn_id: 'turn-1',
        role: 'assistant',
        message_type: 'images',
        status: 'completed',
        content: '',
        asset_ids: ['asset-1'],
        request_ids: ['request-1'],
        created_at: '2026-01-02T00:00:00Z',
      }],
    })

    expect(session.messages[0]).toMatchObject({
      turnId: 'turn-1',
      type: 'images',
      images: [{ assetId: 'asset-1' }],
      requests: [{ id: 'request-1', turnId: 'turn-1' }],
    })
  })

  it('normalizes request details without requiring a key secret', () => {
    expect(normalizeStudioRequest({
      id: 'request-1',
      turn_id: 'turn-1',
      api_key_id: 42,
      api_key_name: 'Studio key',
      endpoint: 'https://api.example.com',
      model: 'gpt-5.5',
      status: 'completed',
      duration_ms: 1200,
      payload: { tools: [{ type: 'image_generation', action: 'generate', size: '1024x1024', aspect_ratio: '1:1', quality: 'low', n: 2 }] },
    })).toMatchObject({
      apiKeyId: 42,
      apiKeyName: 'Studio key',
      image: { action: 'generate', size: '1024x1024', aspectRatio: '1:1', quality: 'low', count: 2 },
    })
  })

  it('parses SSE chunks and confirms server persistence', async () => {
    const encoder = new TextEncoder()
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"type":"response.output_text.delta","delta":"hel'))
        controller.enqueue(encoder.encode('lo"}\n\ndata: {"type":"studio.persisted","request_id":"request-1","message_id":"message-1","status":"completed"}\n\n'))
        controller.enqueue(encoder.encode('data: [DONE]\n\n'))
        controller.close()
      },
    })
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const onTextDelta = vi.fn()
    const onPersisted = vi.fn()

    const persisted = await requestStudioResponse('session-1', {
      turn_id: 'turn-1',
      api_key_id: 42,
      endpoint: 'https://api.example.com/v1',
      payload: {},
    }, { onTextDelta, onPersisted })

    expect(onTextDelta).toHaveBeenCalledWith('hello')
    expect(persisted?.request_id).toBe('request-1')
    expect(onPersisted).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/studio/sessions/session-1/responses', expect.objectContaining({
      method: 'POST',
      headers: expect.objectContaining({ Authorization: 'Bearer jwt-test' }),
      body: JSON.stringify({
        turn_id: 'turn-1',
        api_key_id: 42,
        endpoint: 'https://api.example.com/v1',
        payload: {},
      }),
    }))
    expect(JSON.stringify(fetchMock.mock.calls)).not.toContain('sk-')
  })

  it('uses completed output text when the stream has no deltas', async () => {
    const body = 'data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"final text"}]}]}}\n\ndata: {"type":"studio.persisted","request_id":"request-1"}\n\ndata: [DONE]\n\n'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 200 })))
    const onTextDelta = vi.fn()

    await requestStudioResponse('session-1', {
      turn_id: 'turn-1', api_key_id: 42, endpoint: 'https://api.example.com', payload: {},
    }, { onTextDelta })

    expect(onTextDelta).toHaveBeenCalledOnce()
    expect(onTextDelta).toHaveBeenCalledWith('final text')
  })

  it('surfaces persistence failures from the SSE stream', () => {
    expect(() => consumeResponsesEvent({
      type: 'studio.persistence_failed',
      error: { message: 'disk full' },
    }, {})).toThrow('disk full')
  })
})
