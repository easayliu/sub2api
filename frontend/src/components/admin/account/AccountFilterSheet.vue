<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.filterSheet.title')"
    width="normal"
    :close-on-click-outside="true"
    @close="$emit('close')"
  >
    <div class="space-y-5">
      <section v-for="section in sections" :key="section.key">
        <div class="mb-2 flex items-center justify-between">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ section.label }}</h4>
          <button
            v-if="draft[section.key]"
            type="button"
            class="text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
            @click="setValue(section.key, '')"
          >
            {{ t('admin.accounts.filterSheet.clear') }}
          </button>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="opt in section.options"
            :key="`${section.key}:${String(opt.value)}`"
            type="button"
            class="filter-chip"
            :class="opt.value === (draft[section.key] ?? '') ? 'filter-chip-active' : 'filter-chip-idle'"
            @click="setValue(section.key, opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>
      </section>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary flex-1" @click="reset">
        {{ t('admin.accounts.filterSheet.reset') }}
      </button>
      <button type="button" class="btn btn-primary flex-1" @click="apply">
        {{ t('admin.accounts.filterSheet.apply') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { AdminGroup } from '@/types'

type FilterValue = string | number | boolean | null
type FilterKey = 'platform' | 'type' | 'status' | 'privacy_mode' | 'group'

interface FilterOption {
  value: FilterValue
  label: string
}

interface FilterSection {
  key: FilterKey
  label: string
  options: FilterOption[]
}

const props = defineProps<{
  show: boolean
  filters: Record<string, FilterValue>
  groups?: AdminGroup[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'apply', filters: Record<string, FilterValue>): void
}>()

const { t } = useI18n()

const FILTER_KEYS: FilterKey[] = ['platform', 'type', 'status', 'privacy_mode', 'group']

const draft = reactive<Record<FilterKey, FilterValue>>({
  platform: '',
  type: '',
  status: '',
  privacy_mode: '',
  group: ''
})

const syncDraft = () => {
  for (const k of FILTER_KEYS) {
    draft[k] = (props.filters?.[k] ?? '') as FilterValue
  }
}

watch(
  () => props.show,
  visible => {
    if (visible) syncDraft()
  },
  { immediate: true }
)

const platformOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' }
])

const typeOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') },
  { value: 'bedrock', label: 'AWS Bedrock' }
])

const statusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') },
  { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') },
  { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') },
  { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }
])

const privacyOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])

const groupOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups ?? []).map(g => ({ value: String(g.id), label: g.name }))
])

const sections = computed<FilterSection[]>(() => [
  { key: 'platform', label: t('admin.accounts.filterSheet.platform'), options: platformOptions.value },
  { key: 'type', label: t('admin.accounts.filterSheet.type'), options: typeOptions.value },
  { key: 'status', label: t('admin.accounts.filterSheet.status'), options: statusOptions.value },
  { key: 'privacy_mode', label: t('admin.accounts.filterSheet.privacy'), options: privacyOptions.value },
  { key: 'group', label: t('admin.accounts.filterSheet.group'), options: groupOptions.value }
])

const setValue = (key: FilterKey, value: FilterValue) => {
  draft[key] = value
}

const reset = () => {
  for (const k of FILTER_KEYS) draft[k] = ''
}

const apply = () => {
  const next: Record<string, FilterValue> = { ...props.filters }
  for (const k of FILTER_KEYS) next[k] = draft[k] ?? ''
  emit('apply', next)
  emit('close')
}
</script>

<style scoped>
.filter-chip {
  @apply rounded-full border px-3 py-1.5 text-sm font-medium transition-colors;
  @apply focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-1 dark:focus:ring-offset-dark-800;
}
.filter-chip-idle {
  @apply border-gray-200 bg-white text-gray-700;
  @apply hover:bg-gray-50 active:bg-gray-100;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700;
}
.filter-chip-active {
  @apply border-primary-500 bg-primary-50 text-primary-700;
  @apply dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-200;
}
</style>
