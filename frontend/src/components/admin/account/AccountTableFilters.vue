<template>
  <div class="flex flex-wrap items-center gap-2">
    <SearchInput
      :model-value="searchQuery"
      :placeholder="t('admin.accounts.searchAccounts')"
      class="w-full sm:w-72"
      @update:model-value="$emit('update:searchQuery', $event)"
      @search="$emit('change')"
    />
    <Select
      v-for="cfg in selectConfigs"
      :key="cfg.key"
      :model-value="filters[cfg.key]"
      :options="cfg.options"
      :class="['w-44 filter-select', isActive(cfg.key) && 'is-active']"
      @update:model-value="(value) => updateFilter(cfg.key, value)"
      @change="$emit('change')"
    >
      <template #selected="{ option }">
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="shrink-0 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {{ cfg.label }}
          </span>
          <template v-if="isActive(cfg.key)">
            <span class="text-gray-300 dark:text-gray-600">·</span>
            <span class="truncate text-sm font-medium text-primary-700 dark:text-primary-300">
              {{ option?.label ?? '' }}
            </span>
          </template>
        </span>
      </template>
    </Select>
    <button
      v-if="activeCount > 0"
      type="button"
      class="clear-filters-btn"
      :title="t('admin.accounts.clearAllFilters')"
      @click="clearAllFilters"
    >
      <Icon name="x" size="sm" />
      <span class="hidden lg:inline">{{ t('admin.accounts.clearAllFilters') }}</span>
      <span class="active-count-badge">{{ activeCount }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AdminGroup } from '@/types'

type FilterValue = string | number | boolean | null
type FilterKey = 'platform' | 'type' | 'status' | 'privacy_mode' | 'group'

const props = defineProps<{
  searchQuery: string
  filters: Record<string, FilterValue>
  groups?: AdminGroup[]
}>()

const emit = defineEmits<{
  (e: 'update:searchQuery', value: string): void
  (e: 'update:filters', filters: Record<string, FilterValue>): void
  (e: 'change'): void
}>()

const { t } = useI18n()

const FILTER_KEYS: FilterKey[] = ['platform', 'type', 'status', 'privacy_mode', 'group']

const updateFilter = (key: FilterKey, value: FilterValue) => {
  emit('update:filters', { ...props.filters, [key]: value })
}

const isActive = (key: FilterKey): boolean => {
  const value = props.filters?.[key]
  return value !== '' && value !== null && value !== undefined
}

const activeCount = computed(() => FILTER_KEYS.reduce((n, k) => n + (isActive(k) ? 1 : 0), 0))

const clearAllFilters = () => {
  const next: Record<string, FilterValue> = { ...props.filters }
  for (const k of FILTER_KEYS) next[k] = ''
  emit('update:filters', next)
  emit('change')
}

const platformOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' }
])

const typeOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') },
  { value: 'bedrock', label: 'AWS Bedrock' },
  { value: 'aws-anthropic', label: t('admin.accounts.awsAnthropicLabel') }
])

const statusOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') },
  { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') },
  { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') },
  { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }
])

const privacyOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])

const groupOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups ?? []).map(g => ({ value: String(g.id), label: g.name }))
])

const selectConfigs = computed(() => [
  { key: 'platform' as const, label: t('admin.accounts.filterSheet.platform'), options: platformOptions.value },
  { key: 'type' as const, label: t('admin.accounts.filterSheet.type'), options: typeOptions.value },
  { key: 'status' as const, label: t('admin.accounts.filterSheet.status'), options: statusOptions.value },
  { key: 'privacy_mode' as const, label: t('admin.accounts.filterSheet.privacy'), options: privacyOptions.value },
  { key: 'group' as const, label: t('admin.accounts.filterSheet.group'), options: groupOptions.value }
])
</script>

<style scoped>
.filter-select.is-active :deep(.select-trigger) {
  @apply border-primary-500/60 bg-primary-50/60 ring-1 ring-primary-500/15;
  @apply dark:border-primary-400/50 dark:bg-primary-900/15 dark:ring-primary-400/15;
}

.filter-select.is-active :deep(.select-trigger:hover) {
  @apply border-primary-500/80 dark:border-primary-400/70;
}

.clear-filters-btn {
  @apply inline-flex items-center gap-1.5 rounded-xl border px-3 py-2.5 text-sm font-medium;
  @apply border-gray-200 bg-white text-gray-600 transition-all duration-200;
  @apply hover:border-red-300 hover:bg-red-50 hover:text-red-600;
  @apply focus:outline-none focus:ring-2 focus:ring-red-500/30;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300;
  @apply dark:hover:border-red-500/40 dark:hover:bg-red-500/10 dark:hover:text-red-300;
}

.active-count-badge {
  @apply ml-0.5 inline-flex h-5 min-w-[20px] items-center justify-center rounded-full px-1.5;
  @apply bg-primary-100 text-xs font-semibold text-primary-700;
  @apply dark:bg-primary-900/40 dark:text-primary-200;
}
</style>
