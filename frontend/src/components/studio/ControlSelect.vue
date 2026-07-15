<template>
  <label class="min-w-0">
    <span class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ label }}</span>
    <Select :model-value="modelValue" :options="options" class="w-full" @update:model-value="$emit('update:modelValue', $event)" />
  </label>
</template>

<script setup lang="ts">
import Select from '@/components/common/Select.vue'

interface SelectOption {
  [key: string]: unknown
  value: string | number | boolean | null
  label: string
  disabled?: boolean
}

defineProps<{
  modelValue: string | number | boolean | null
  label: string
  options: SelectOption[]
}>()

defineEmits<{
  'update:modelValue': [value: string | number | boolean | null]
}>()
</script>
