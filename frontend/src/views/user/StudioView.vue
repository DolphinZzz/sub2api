<template>
  <AppLayout>
    <div class="studio-shell overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
      <aside
        class="session-panel border-b border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-950/60 lg:border-b-0 lg:border-r"
      >
        <div class="flex h-16 items-center justify-between border-b border-gray-200 px-4 dark:border-dark-700">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('studio.sessions') }}</h2>
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('studio.serverHistory') }}</p>
          </div>
          <button class="btn btn-primary btn-icon !rounded-lg !p-2" :disabled="isGenerating || sessionsLoading" :title="t('studio.newSession')" @click="createSession(mode)">
            <Icon name="plus" size="sm" />
          </button>
        </div>

        <div class="session-list flex gap-2 overflow-x-auto p-3 lg:block lg:space-y-1.5 lg:overflow-y-auto">
          <button
            v-for="session in sessions"
            :key="session.id"
            type="button"
            class="session-item group relative min-w-56 flex-1 rounded-lg border p-3 text-left transition-colors lg:min-w-0 lg:w-full"
            :class="session.id === activeSessionId
              ? 'border-primary-300 bg-white shadow-sm dark:border-primary-700 dark:bg-dark-800'
              : 'border-transparent hover:border-gray-200 hover:bg-white dark:hover:border-dark-700 dark:hover:bg-dark-800/70'"
            :disabled="isGenerating && session.id !== activeSessionId"
            @click="selectSession(session.id)"
          >
            <div class="flex items-start gap-2.5 pr-6">
              <span
                class="mt-0.5 flex h-7 w-7 flex-none items-center justify-center rounded-lg"
                :class="session.mode === 'image'
                  ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
                  : 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'"
              >
                <Icon :name="session.mode === 'image' ? 'sparkles' : 'chat'" size="sm" />
              </span>
              <span class="min-w-0">
                <span class="block truncate text-sm font-medium text-gray-800 dark:text-gray-100">{{ session.title }}</span>
                <span class="mt-1 block text-xs text-gray-400 dark:text-dark-500">{{ formatSessionTime(session.updatedAt) }}</span>
              </span>
            </div>
            <span
              role="button"
              tabindex="0"
              class="absolute right-2 top-2 rounded p-1 text-gray-400 opacity-0 hover:bg-red-50 hover:text-red-500 focus:opacity-100 group-hover:opacity-100 dark:hover:bg-red-950/30"
              :title="t('common.delete')"
              @click.stop="removeSession(session.id)"
              @keydown.enter.stop="removeSession(session.id)"
            >
              <Icon name="trash" size="sm" />
            </span>
          </button>
        </div>
      </aside>

      <section class="min-w-0 bg-white dark:bg-dark-900">
        <header class="border-b border-gray-200 bg-white/95 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/95 md:px-5">
          <div class="flex flex-col gap-3 xl:flex-row xl:items-end xl:justify-between">
            <div class="flex items-center justify-between gap-4">
              <div>
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('studio.title') }}</h2>
                <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('studio.subtitle') }}</p>
              </div>
              <div class="flex h-9 flex-none items-center rounded-lg bg-gray-100 p-1 dark:bg-dark-800" role="tablist">
                <button
                  type="button"
                  class="flex h-7 items-center gap-1.5 rounded-md px-3 text-xs font-medium transition-colors"
                  :class="mode === 'chat' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 dark:text-dark-400'"
                  @click="setMode('chat')"
                >
                  <Icon name="chat" size="sm" />
                  {{ t('studio.chatMode') }}
                </button>
                <button
                  type="button"
                  class="flex h-7 items-center gap-1.5 rounded-md px-3 text-xs font-medium transition-colors"
                  :class="mode === 'image' ? 'bg-white text-amber-700 shadow-sm dark:bg-dark-700 dark:text-amber-300' : 'text-gray-500 dark:text-dark-400'"
                  @click="setMode('image')"
                >
                  <Icon name="sparkles" size="sm" />
                  {{ t('studio.imageMode') }}
                </button>
              </div>
            </div>

            <div class="grid min-w-0 flex-1 grid-cols-1 gap-2 sm:grid-cols-3 xl:max-w-3xl">
              <label class="min-w-0">
                <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('studio.endpoint') }}</span>
                <Select v-model="selectedEndpoint" :options="endpointOptions" class="w-full">
                  <template #selected="{ option }">
                    <span class="block truncate">{{ option?.label || t('studio.defaultEndpoint') }}</span>
                  </template>
                  <template #option="{ option }">
                    <span class="min-w-0 flex-1">
                      <span class="block truncate text-sm">{{ option.label }}</span>
                      <span class="block truncate text-xs text-gray-400">{{ option.description }}</span>
                    </span>
                  </template>
                </Select>
              </label>
              <label class="min-w-0">
                <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('studio.apiKeyQuota') }}</span>
                <Select v-model="selectedKeyId" :options="apiKeyOptions" :disabled="keysLoading" class="w-full">
                  <template #selected="{ option }">
                    <span class="block truncate">{{ option?.label || (keysLoading ? t('common.loading') : t('studio.noApiKey')) }}</span>
                  </template>
                  <template #option="{ option }">
                    <span class="min-w-0 flex-1">
                      <span class="block truncate text-sm">{{ option.label }}</span>
                      <span v-if="option.description" class="block text-xs text-gray-400">{{ option.description }}</span>
                    </span>
                  </template>
                </Select>
              </label>
              <label class="min-w-0">
                <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('studio.model') }}</span>
                <Select v-if="mode === 'chat'" v-model="selectedModel" :options="modelOptions" class="w-full" />
                <Select v-else model-value="gpt-image-2" :options="imageModelOptions" :disabled="true" class="w-full" />
              </label>
            </div>
          </div>

          <div v-if="mode === 'image'" class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700">
            <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <ControlSelect v-model="imageAction" :label="t('studio.action')" :options="actionOptions" />
              <ControlSelect v-model="imageSize" :label="t('studio.size')" :options="sizeOptions" />
              <ControlSelect v-model="aspectRatio" :label="t('studio.ratio')" :options="ratioOptions" />
              <ControlSelect v-model="imageQuality" :label="t('studio.quality')" :options="qualityOptions" />
              <ControlSelect v-model="imageBackground" :label="t('studio.background')" :options="backgroundOptions" />
              <ControlSelect v-model="outputFormat" :label="t('studio.format')" :options="formatOptions" />

              <label>
                <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('studio.count') }}</span>
                <div class="flex h-[42px] items-center justify-between rounded-xl border border-gray-200 bg-white px-1 dark:border-dark-600 dark:bg-dark-800">
                  <button type="button" class="h-8 w-8 rounded-lg text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="imageCount <= 1" @click="imageCount--">-</button>
                  <span class="w-8 text-center text-sm font-semibold tabular-nums">{{ imageCount }}</span>
                  <button type="button" class="h-8 w-8 rounded-lg text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="imageCount >= 8" @click="imageCount++">+</button>
                </div>
              </label>

              <label>
                <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('studio.reference') }}</span>
                <button
                  type="button"
                  class="flex h-[42px] w-full items-center justify-center gap-2 rounded-xl border border-dashed border-gray-300 px-2 text-xs font-medium text-gray-600 hover:border-primary-400 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:text-gray-300"
                  :disabled="referenceImages.length >= maxReferenceImages"
                  @click="referenceInput?.click()"
                >
                  <Icon :name="referenceImages.length ? 'check' : 'upload'" size="sm" />
                  <span class="truncate">{{ referenceImages.length ? t('studio.referenceCount', { count: referenceImages.length, max: maxReferenceImages }) : t('studio.upload') }}</span>
                </button>
                <input ref="referenceInput" class="hidden" type="file" accept="image/png,image/jpeg,image/webp" multiple @change="onReferenceSelected">
              </label>
            </div>

            <div v-if="referenceImages.length" class="mt-2 flex flex-wrap gap-2">
              <div v-for="(reference, index) in referenceImages" :key="reference.id" class="flex min-w-0 max-w-44 items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 p-1.5 dark:border-dark-700 dark:bg-dark-800">
                <img :src="reference.preview" :alt="reference.fileName" class="h-10 w-10 flex-none rounded-md object-cover">
                <span class="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-dark-400" :title="reference.fileName">{{ reference.fileName }}</span>
                <button type="button" class="flex-none rounded-md p-1 text-gray-400 hover:bg-gray-200 hover:text-red-500 dark:hover:bg-dark-700" :title="t('studio.removeReference')" @click="removeReference(index)">
                  <Icon name="x" size="sm" />
                </button>
              </div>
            </div>
          </div>
        </header>

        <div ref="messagesContainer" class="messages-area overflow-y-auto px-4 py-5 md:px-6">
          <div v-if="!currentSession?.messages.length" class="flex h-full min-h-72 flex-col items-center justify-center text-center">
            <span class="flex h-14 w-14 items-center justify-center rounded-2xl bg-gray-100 text-primary-600 dark:bg-dark-800 dark:text-primary-300">
              <Icon :name="mode === 'image' ? 'sparkles' : 'chat'" size="xl" />
            </span>
            <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ mode === 'image' ? t('studio.imageEmptyTitle') : t('studio.chatEmptyTitle') }}</h3>
            <p class="mt-1 max-w-md text-sm text-gray-500 dark:text-dark-400">{{ mode === 'image' ? t('studio.imageEmptyHint') : t('studio.chatEmptyHint') }}</p>
          </div>

          <div v-else class="mx-auto max-w-4xl space-y-5">
            <article v-for="message in currentSession.messages" :key="message.id" class="flex gap-3" :class="message.role === 'user' ? 'justify-end' : 'justify-start'">
              <span v-if="message.role === 'assistant'" class="mt-1 flex h-8 w-8 flex-none items-center justify-center rounded-lg bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                <Icon :name="message.type === 'images' ? 'sparkles' : 'chat'" size="sm" />
              </span>
              <div class="min-w-0" :class="message.type === 'images' ? 'w-full max-w-3xl' : 'max-w-[min(82%,46rem)]'">
                <div
                  v-if="message.type === 'text'"
                  class="message-text rounded-xl px-4 py-3 text-sm leading-6"
                  :class="message.role === 'user'
                    ? 'bg-primary-600 text-white'
                    : 'border border-gray-200 bg-gray-50 text-gray-800 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-100'"
                >
                  <div v-if="message.role === 'assistant'" class="markdown-body" v-html="renderMarkdown(message.content || t('studio.thinking'))"></div>
                  <p v-else class="whitespace-pre-wrap break-words">{{ message.content }}</p>
                </div>
                <div v-else-if="message.type === 'error'" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300">
                  {{ message.content }}
                </div>
                <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <figure v-for="(image, index) in message.images" :key="`${message.id}-${index}`" class="overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800">
                    <div v-if="!image.blobUrl" class="flex aspect-square items-center justify-center text-sm text-gray-400">
                      <Icon name="refresh" size="sm" class="mr-2 animate-spin" />
                      {{ t('common.loading') }}
                    </div>
                    <img v-else :src="image.blobUrl" :alt="`${t('studio.generatedImage')} ${index + 1}`" class="aspect-square w-full object-contain">
                    <figcaption class="flex items-center justify-between gap-2 border-t border-gray-200 px-3 py-2 dark:border-dark-700">
                      <span class="truncate text-xs text-gray-500 dark:text-dark-400">{{ image.revisedPrompt || `${outputFormat.toUpperCase()} · ${imageSize}` }}</span>
                      <button type="button" class="rounded-lg p-1.5 text-gray-500 hover:bg-white hover:text-primary-600 dark:hover:bg-dark-700" :title="t('studio.download')" @click="downloadImage(image, index)">
                        <Icon name="download" size="sm" />
                      </button>
                    </figcaption>
                  </figure>
                  <div v-if="isGenerating && !message.images?.length" class="flex aspect-video items-center justify-center rounded-lg border border-dashed border-gray-300 bg-gray-50 text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-400">
                    <Icon name="refresh" size="sm" class="mr-2 animate-spin" />
                    {{ t('studio.generating') }}
                  </div>
                </div>
                <div v-if="message.role === 'assistant' && message.requests?.length" class="mt-2 space-y-1.5">
                  <details
                    v-for="request in message.requests"
                    :key="request.id"
                    class="rounded-lg border border-gray-200 bg-white text-xs dark:border-dark-700 dark:bg-dark-800"
                    @toggle="loadRequestDetails(request.id)"
                  >
                    <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-2 text-gray-600 dark:text-dark-300">
                      <span class="flex min-w-0 items-center gap-2">
                        <Icon name="document" size="sm" />
                        <span class="truncate">{{ t('studio.requestDetails') }}</span>
                      </span>
                      <span :class="requestStatusClass(requestDetails[request.id]?.status || request.status)">
                        {{ requestDetails[request.id]?.status || request.status }}
                      </span>
                    </summary>
                    <div class="grid gap-x-5 gap-y-2 border-t border-gray-100 px-3 py-3 text-gray-600 dark:border-dark-700 dark:text-dark-300 sm:grid-cols-2">
                      <template v-if="requestDetails[request.id] || request">
                        <div><span class="text-gray-400">{{ t('studio.requestEndpoint') }}:</span> {{ (requestDetails[request.id] || request).endpoint || '-' }}</div>
                        <div><span class="text-gray-400">{{ t('studio.requestApiKey') }}:</span> {{ requestKeyLabel(requestDetails[request.id] || request) }}</div>
                        <div><span class="text-gray-400">{{ t('studio.model') }}:</span> {{ (requestDetails[request.id] || request).model || '-' }}</div>
                        <div><span class="text-gray-400">{{ t('studio.requestDuration') }}:</span> {{ formatDuration((requestDetails[request.id] || request).durationMs) }}</div>
                        <div v-if="(requestDetails[request.id] || request).image" class="sm:col-span-2">
                          <span class="text-gray-400">{{ t('studio.imageParameters') }}:</span>
                          {{ formatImageParameters((requestDetails[request.id] || request).image) }}
                        </div>
                        <div v-if="(requestDetails[request.id] || request).errorMessage" class="text-red-600 dark:text-red-300 sm:col-span-2">
                          <span class="text-red-400">{{ t('studio.requestError') }}:</span>
                          {{ (requestDetails[request.id] || request).errorMessage }}
                        </div>
                      </template>
                    </div>
                  </details>
                </div>
              </div>
            </article>
          </div>
        </div>

        <footer class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 md:p-4">
          <div class="mx-auto max-w-4xl">
            <div class="flex items-end gap-2 rounded-xl border border-gray-200 bg-gray-50 p-2 focus-within:border-primary-400 focus-within:ring-2 focus-within:ring-primary-500/20 dark:border-dark-700 dark:bg-dark-800">
              <textarea
                ref="promptInput"
                v-model="prompt"
                rows="2"
                class="min-h-[48px] max-h-40 flex-1 resize-none bg-transparent px-2 py-2 text-sm text-gray-900 outline-none placeholder:text-gray-400 dark:text-white"
                :placeholder="mode === 'image' ? t('studio.imagePlaceholder') : t('studio.chatPlaceholder')"
                :disabled="isGenerating"
                @keydown.enter.exact.prevent="submit"
              ></textarea>
              <button v-if="isGenerating" type="button" class="btn btn-secondary btn-icon !rounded-lg !p-2.5" :title="t('studio.stop')" @click="stopGeneration">
                <span class="h-3 w-3 rounded-sm bg-red-500"></span>
              </button>
              <button v-else type="button" class="btn btn-primary btn-icon !rounded-lg !p-2.5" :disabled="!canSubmit" :title="mode === 'image' ? t('studio.generate') : t('studio.send')" @click="submit">
                <Icon :name="mode === 'image' ? 'sparkles' : 'arrowUp'" size="sm" />
              </button>
            </div>
            <p v-if="mode === 'image' && imageAction === 'edit' && !referenceImages.length" class="mt-1.5 text-xs text-amber-600 dark:text-amber-400">{{ t('studio.editNeedsImage') }}</p>
          </div>
        </footer>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import ControlSelect from '@/components/studio/ControlSelect.vue'
import { keysAPI } from '@/api/keys'
import { buildGatewayUrl } from '@/api/client'
import {
  buildAsyncImagePayload,
  buildChatPayload,
  createStudioSession,
  deleteStudioSession,
  fetchStudioAsset,
  getStudioRequest,
  getStudioSession,
  imageAspectRatioForSize,
  imageSizeForAspectRatio,
  listStudioSessions,
  requestStudioResponse,
  submitStudioImage,
  STUDIO_IMAGE_ASPECT_RATIOS,
  STUDIO_IMAGE_SIZES,
  waitForStudioImageTask,
} from '@/api/studio'
import { useAppStore } from '@/stores/app'
import type { ApiKey } from '@/types'
import {
  clearLegacyStudioStorage,
  type StudioImageData,
  type StudioMessage,
  type StudioMode,
  type StudioRequestSummary,
  type StudioSession,
} from '@/utils/studioStorage'

const { t, locale } = useI18n()
const appStore = useAppStore()

const sessions = ref<StudioSession[]>([])
const activeSessionId = ref('')
const sessionsLoading = ref(false)
const apiKeys = ref<ApiKey[]>([])
const keysLoading = ref(false)
const requestDetails = ref<Record<string, StudioRequestSummary>>({})
const selectedEndpoint = ref('')
const selectedKeyId = ref<string | number | null>(null)
const selectedModel = ref('gpt-5.5')
const imageAction = ref<'generate' | 'edit'>('generate')
const imageSize = ref('1024x1024')
const aspectRatio = ref('1:1')
const imageQuality = ref<'low' | 'medium' | 'high'>('low')
const imageBackground = ref<'auto' | 'transparent'>('auto')
const outputFormat = ref<'png' | 'jpeg' | 'webp'>('png')
const imageCount = ref(1)
const prompt = ref('')
const maxReferenceImages = 5
const supportedReferenceTypes = new Set(['image/jpeg', 'image/png', 'image/webp'])
const referenceImages = ref<Array<{ id: string; preview: string; fileName: string }>>([])
const referenceInput = ref<HTMLInputElement | null>(null)
const promptInput = ref<HTMLTextAreaElement | null>(null)
const messagesContainer = ref<HTMLElement | null>(null)
const isGenerating = ref(false)
let requestController: AbortController | null = null
let sessionLoadSequence = 0
const assetObjectURLs = new Map<string, string>()

const currentSession = computed(() => sessions.value.find((session) => session.id === activeSessionId.value))
const mode = computed<StudioMode>(() => currentSession.value?.mode || 'chat')
const selectedKey = computed(() => apiKeys.value.find((key) => key.id === Number(selectedKeyId.value)))

const modelOptions = [
  { value: 'gpt-5.4', label: 'gpt-5.4' },
  { value: 'gpt-5.5', label: 'gpt-5.5' },
  { value: 'gpt-5.6-sol', label: 'gpt-5.6 sol' },
  { value: 'gpt-5.6-terra', label: 'gpt-5.6 terra' },
  { value: 'gpt-5.6-luna', label: 'gpt-5.6 luna' },
]
const imageModelOptions = [{ value: 'gpt-image-2', label: 'gpt-image-2' }]
const sizeOptions = STUDIO_IMAGE_SIZES.map((value) => ({ value, label: value.replace('x', ' × ') }))
const ratioOptions = ['1:1', '2:3', '3:2', '3:4', '4:3', '5:4', '4:5', '9:16', '16:9', '9:21', '21:9']
  .map((value) => ({ value, label: value, disabled: !STUDIO_IMAGE_ASPECT_RATIOS.includes(value) }))
const actionOptions = computed(() => [
  { value: 'generate', label: t('studio.generateMode') },
  { value: 'edit', label: t('studio.editMode') },
])
const qualityOptions = computed(() => [
  { value: 'low', label: t('studio.low') },
  { value: 'medium', label: t('studio.medium') },
  { value: 'high', label: t('studio.high') },
])
const backgroundOptions = computed(() => [
  { value: 'auto', label: t('studio.auto') },
  { value: 'transparent', label: t('studio.transparent') },
])
const formatOptions = [
  { value: 'png', label: 'PNG' },
  { value: 'jpeg', label: 'JPEG' },
  { value: 'webp', label: 'WebP' },
]

const endpointOptions = computed(() => {
  const settings = appStore.cachedPublicSettings
  const options: Array<{ value: string; label: string; description: string }> = []
  const defaultEndpoint = (settings?.api_base_url || buildGatewayUrl('/v1')).trim()
  if (defaultEndpoint) {
    options.push({
      value: defaultEndpoint,
      label: t('studio.defaultEndpoint'),
      description: defaultEndpoint,
    })
  }
  for (const item of settings?.custom_endpoints || []) {
    const endpoint = item.endpoint.trim()
    if (endpoint && !options.some((option) => option.value === endpoint)) {
      options.push({
        value: endpoint,
        label: item.name.trim() || endpoint,
        description: item.description.trim() ? `${endpoint} · ${item.description.trim()}` : endpoint,
      })
    }
  }
  return options
})

const apiKeyOptions = computed(() => apiKeys.value.map((key) => {
  const remaining = key.quota === 0 ? t('studio.unlimited') : `$${Math.max(0, key.quota - key.quota_used).toFixed(2)}`
  return {
    value: key.id,
    label: `${key.name} · ${remaining}`,
    description: `${key.key.slice(0, 8)}••••${key.key.slice(-4)}`,
  }
}))

const canSubmit = computed(() => {
  if (!prompt.value.trim() || !selectedKey.value || !selectedEndpoint.value) return false
  return mode.value !== 'image' || imageAction.value !== 'edit' || referenceImages.value.length > 0
})

function makeId(prefix: string) {
  return `${prefix}_${crypto.randomUUID()}`
}

async function createSession(sessionMode: StudioMode = 'chat') {
  if (isGenerating.value || sessionsLoading.value) return
  sessionsLoading.value = true
  try {
    const session = await createStudioSession(sessionMode, t('studio.newSession'))
    sessions.value.unshift(session)
    await selectSession(session.id)
    nextTick(() => promptInput.value?.focus())
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    sessionsLoading.value = false
  }
}

async function selectSession(id: string) {
  if (isGenerating.value && id !== activeSessionId.value) return
  activeSessionId.value = id
  clearReference()
  const sequence = ++sessionLoadSequence
  try {
    const session = await getStudioSession(id)
    if (sequence !== sessionLoadSequence || activeSessionId.value !== id) return
    await hydrateSessionAssets(session)
    replaceSession(session)
    nextTick(scrollToBottom)
  } catch (error) {
    if (sequence === sessionLoadSequence) appStore.showError(errorMessage(error))
  }
}

async function removeSession(id: string) {
  if (isGenerating.value) return
  try {
    await deleteStudioSession(id)
    const deleted = sessions.value.find((session) => session.id === id)
    if (deleted) releaseSessionAssets(deleted)
    sessions.value = sessions.value.filter((session) => session.id !== id)
    if (activeSessionId.value === id) {
      if (sessions.value[0]) await selectSession(sessions.value[0].id)
      else await createSession('chat')
    }
  } catch (error) {
    appStore.showError(errorMessage(error))
  }
}

function setMode(nextMode: StudioMode) {
  const session = currentSession.value
  if (!session || session.mode === nextMode || isGenerating.value) return
  session.mode = nextMode
}

function formatSessionTime(timestamp: number) {
  return new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(timestamp)
}

function renderMarkdown(content: string) {
  return DOMPurify.sanitize(marked.parse(content, { breaks: true }) as string)
}

function scrollToBottom() {
  const container = messagesContainer.value
  if (container) container.scrollTop = container.scrollHeight
}

function appendMessage(message: Omit<StudioMessage, 'id' | 'createdAt'>): StudioMessage {
  const result: StudioMessage = { ...message, id: makeId('message'), createdAt: Date.now() }
  currentSession.value?.messages.push(result)
  nextTick(scrollToBottom)
  return result
}

function updateSessionTitle(text: string) {
  const session = currentSession.value
  if (!session || session.messages.filter((message) => message.role === 'user').length !== 1) return
  session.title = text.replace(/\s+/g, ' ').trim().slice(0, 32) || t('studio.newSession')
}

async function submit() {
  if (!canSubmit.value || isGenerating.value) return
  if (mode.value === 'chat') await sendChat()
  else await generateImages()
}

async function sendChat() {
  const session = currentSession.value
  const key = selectedKey.value
  const text = prompt.value.trim()
  if (!session || !key || !text) return

  prompt.value = ''
  const turnId = makeId('turn')
  appendMessage({ role: 'user', type: 'text', content: text, turnId })
  updateSessionTitle(text)
  const assistant = appendMessage({ role: 'assistant', type: 'text', content: '', turnId })
  const input = session.messages
    .filter((message) => message.type === 'text' && message.content)
    .map((message) => ({ role: message.role, text: message.content }))

  isGenerating.value = true
  const controller = new AbortController()
  requestController = controller
  try {
    const persisted = await requestStudioResponse(
      session.id,
      {
        turn_id: turnId,
        api_key_id: key.id,
        endpoint: selectedEndpoint.value,
        payload: buildChatPayload(selectedModel.value, input),
      },
      { onTextDelta: (delta) => { assistant.content += delta; nextTick(scrollToBottom) } },
      controller.signal,
    )
    if (!persisted) throw new Error(t('studio.persistenceFailed'))
    await reloadSession(session.id)
  } catch (error) {
    await reloadSession(session.id).catch(() => undefined)
    const currentAssistant = currentSession.value?.messages.find((message) => message.turnId === turnId && message.role === 'assistant') || assistant
    if (!currentAssistant.content) {
      currentAssistant.type = 'error'
      currentAssistant.content = controller.signal.aborted ? t('studio.stopped') : errorMessage(error)
    }
  } finally {
    isGenerating.value = false
    requestController = null
  }
}

async function generateImages() {
  const session = currentSession.value
  const key = selectedKey.value
  const text = prompt.value.trim()
  if (!session || !key || !text) return

  prompt.value = ''
  const turnId = makeId('turn')
  appendMessage({ role: 'user', type: 'text', content: text, turnId })
  updateSessionTitle(text)
  const assistant = appendMessage({ role: 'assistant', type: 'images', content: '', images: [], turnId })
  isGenerating.value = true
  const controller = new AbortController()
  requestController = controller
  let received = 0

  try {
    for (let index = 0; index < imageCount.value; index += 1) {
      const task = await submitStudioImage(
        session.id,
        {
          turn_id: turnId,
          api_key_id: key.id,
          endpoint: selectedEndpoint.value,
          payload: buildAsyncImagePayload(text, {
            action: imageAction.value,
            size: imageSize.value,
            aspectRatio: aspectRatio.value,
            quality: imageQuality.value,
            background: imageBackground.value,
            outputFormat: outputFormat.value,
            referenceImages: referenceImages.value.map((reference) => reference.preview),
          }),
        },
        controller.signal,
      )
      if (!task.requestId) throw new Error(t('studio.persistenceFailed'))
      await waitForStudioImageTask(task.requestId, controller.signal)
      const refreshed = await reloadSession(session.id)
      const images = refreshed.messages.find((message) => message.turnId === turnId && message.role === 'assistant')?.images || []
      if (images.length <= received) throw new Error(t('studio.noImageReturned'))
      received = images.length
    }
  } catch (error) {
    await reloadSession(session.id).catch(() => undefined)
    const persistedAssistant = currentSession.value?.messages.find((message) => message.turnId === turnId && message.role === 'assistant')
    if (!(persistedAssistant?.images?.length || assistant.images?.length)) {
      const target = persistedAssistant || assistant
      target.type = 'error'
      target.content = controller.signal.aborted ? t('studio.stopped') : errorMessage(error)
    } else if (!controller.signal.aborted) {
      appendMessage({ role: 'assistant', type: 'error', content: errorMessage(error) })
    }
  } finally {
    isGenerating.value = false
    requestController = null
  }
}

function stopGeneration() {
  requestController?.abort()
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : t('studio.requestFailed')
}

async function onReferenceSelected(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (!files.length) return

  const next: Array<{ id: string; preview: string; fileName: string }> = []
  let limitReached = false
  for (const file of files) {
    if (referenceImages.value.length + next.length >= maxReferenceImages) {
      limitReached = true
      break
    }
    if (!supportedReferenceTypes.has(file.type)) {
      appStore.showError(t('studio.invalidImage'))
      continue
    }
    if (file.size > 20 * 1024 * 1024) {
      appStore.showError(t('studio.imageTooLarge'))
      continue
    }
    try {
      next.push({ id: crypto.randomUUID(), preview: await readReferenceImage(file), fileName: file.name })
    } catch {
      appStore.showError(t('studio.invalidImage'))
    }
  }
  referenceImages.value = [...referenceImages.value, ...next]
  if (limitReached) appStore.showError(t('studio.referenceLimit', { max: maxReferenceImages }))
}

function readReferenceImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error || new Error('Failed to read reference image'))
    reader.onload = () => resolve(String(reader.result || ''))
    reader.readAsDataURL(file)
  })
}

function removeReference(index: number) {
  referenceImages.value = referenceImages.value.filter((_, currentIndex) => currentIndex !== index)
}

function clearReference() {
  referenceImages.value = []
  if (referenceInput.value) referenceInput.value.value = ''
}

async function downloadImage(image: StudioImageData, index: number) {
  let temporaryURL = ''
  try {
    const href = image.blobUrl || (temporaryURL = URL.createObjectURL(await fetchStudioAsset(image.assetId)))
    const link = document.createElement('a')
    link.href = href
    link.download = `studio-${Date.now()}-${index + 1}.${image.format === 'jpeg' ? 'jpg' : image.format}`
    link.click()
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    if (temporaryURL) URL.revokeObjectURL(temporaryURL)
  }
}

function replaceSession(session: StudioSession) {
  const index = sessions.value.findIndex((item) => item.id === session.id)
  if (index === -1) sessions.value.push(session)
  else sessions.value[index] = session
  sessions.value.sort((a, b) => b.updatedAt - a.updatedAt)
}

async function reloadSession(id: string): Promise<StudioSession> {
  const session = await getStudioSession(id)
  await hydrateSessionAssets(session)
  replaceSession(session)
  return session
}

async function hydrateSessionAssets(session: StudioSession) {
  const images = session.messages.flatMap((message) => message.images || [])
  await Promise.all(images.map(async (image) => {
    const cached = assetObjectURLs.get(image.assetId)
    if (cached) {
      image.blobUrl = cached
      return
    }
    try {
      const blob = await fetchStudioAsset(image.assetId)
      const url = URL.createObjectURL(blob)
      image.mimeType ||= blob.type || undefined
      image.format = blob.type.split('/')[1] || image.format
      assetObjectURLs.set(image.assetId, url)
      image.blobUrl = url
    } catch (error) {
      console.error('Failed to load Studio asset:', error)
    }
  }))
}

function releaseSessionAssets(session: StudioSession) {
  for (const image of session.messages.flatMap((message) => message.images || [])) {
    const url = assetObjectURLs.get(image.assetId)
    if (url) URL.revokeObjectURL(url)
    assetObjectURLs.delete(image.assetId)
  }
}

async function loadRequestDetails(id: string) {
  if (!id || requestDetails.value[id]) return
  try {
    requestDetails.value[id] = await getStudioRequest(id)
  } catch (error) {
    appStore.showError(errorMessage(error))
  }
}

function requestKeyLabel(request: StudioRequestSummary) {
  const id = request.apiKeyId ? `#${request.apiKeyId}` : '-'
  return request.apiKeyName ? `${request.apiKeyName} (${id})` : id
}

function formatDuration(value: number | null) {
  return value === null ? '-' : `${value} ms`
}

function formatImageParameters(image: StudioRequestSummary['image']) {
  if (!image) return '-'
  return [image.action, image.size, image.aspectRatio, image.quality, image.background, image.outputFormat, image.count ? `× ${image.count}` : '']
    .filter(Boolean)
    .join(' · ')
}

function requestStatusClass(status: string) {
  if (status === 'completed') return 'text-emerald-600 dark:text-emerald-300'
  if (status === 'running') return 'text-amber-600 dark:text-amber-300'
  return 'text-red-600 dark:text-red-300'
}

async function loadKeys() {
  keysLoading.value = true
  try {
    const first = await keysAPI.list(1, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    apiKeys.value = first.items
    for (let page = 2; page <= Math.min(first.pages, 20); page += 1) {
      const response = await keysAPI.list(page, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
      apiKeys.value.push(...response.items)
    }
    if (!apiKeys.value.some((key) => key.id === Number(selectedKeyId.value))) {
      selectedKeyId.value = apiKeys.value[0]?.id ?? null
    }
  } catch (error) {
    console.error('Failed to load API keys:', error)
    appStore.showError(t('studio.keysFailed'))
  } finally {
    keysLoading.value = false
  }
}

watch(endpointOptions, (options) => {
  if (!options.some((option) => option.value === selectedEndpoint.value)) {
    selectedEndpoint.value = options[0]?.value || window.location.origin
  }
}, { immediate: true })

watch(imageSize, (size) => {
  const matchingRatio = imageAspectRatioForSize(size)
  if (aspectRatio.value !== matchingRatio) aspectRatio.value = matchingRatio
})
watch(aspectRatio, (ratio) => {
  const matchingSize = imageSizeForAspectRatio(imageSize.value, ratio)
  if (imageSize.value !== matchingSize) imageSize.value = matchingSize
})

watch(outputFormat, (format) => {
  if (format === 'jpeg' && imageBackground.value === 'transparent') imageBackground.value = 'auto'
})
watch(imageBackground, (background) => {
  if (background === 'transparent' && outputFormat.value === 'jpeg') outputFormat.value = 'png'
})

onMounted(async () => {
  await appStore.fetchPublicSettings(true)
  sessionsLoading.value = true
  try {
    await clearLegacyStudioStorage()
    sessions.value = await listStudioSessions()
  } catch (error) {
    console.error('Failed to load studio sessions:', error)
    appStore.showError(errorMessage(error))
  } finally {
    sessionsLoading.value = false
  }
  if (!sessions.value.length) await createSession('chat')
  else await selectSession(sessions.value[0].id)
  await loadKeys()
  nextTick(scrollToBottom)
})

onBeforeUnmount(() => {
  requestController?.abort()
  for (const url of assetObjectURLs.values()) URL.revokeObjectURL(url)
  assetObjectURLs.clear()
})
</script>

<style scoped>
.studio-shell {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: calc(100vh - 8rem);
}

.studio-shell > section {
  display: grid;
  grid-template-rows: auto minmax(20rem, 1fr) auto;
  min-height: 0;
}

.session-list {
  max-height: 9.5rem;
}

.messages-area {
  min-height: 20rem;
  max-height: calc(100vh - 22rem);
}

.markdown-body :deep(p + p),
.markdown-body :deep(ul),
.markdown-body :deep(ol),
.markdown-body :deep(pre) {
  margin-top: 0.75rem;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 1.25rem;
}

.markdown-body :deep(ul) { list-style: disc; }
.markdown-body :deep(ol) { list-style: decimal; }
.markdown-body :deep(code) {
  border-radius: 0.25rem;
  background: rgba(15, 23, 42, 0.08);
  padding: 0.1rem 0.3rem;
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
}
.dark .markdown-body :deep(code) { background: rgba(255, 255, 255, 0.08); }
.markdown-body :deep(pre) {
  overflow-x: auto;
  border-radius: 0.5rem;
  background: #111827;
  padding: 0.875rem;
  color: #e5e7eb;
}
.markdown-body :deep(pre code) { background: transparent; padding: 0; }

@media (min-width: 1024px) {
  .studio-shell {
    grid-template-columns: 17rem minmax(0, 1fr);
    height: calc(100vh - 8rem);
  }

  .session-panel,
  .session-list {
    min-height: 0;
  }

  .session-panel {
    display: grid;
    grid-template-rows: auto minmax(0, 1fr);
  }

  .studio-shell > section {
    grid-template-rows: auto minmax(0, 1fr) auto;
  }

  .session-list,
  .messages-area {
    max-height: none;
  }

  .messages-area {
    min-height: 0;
  }
}
</style>
