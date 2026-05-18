<template>
  <div class="flex flex-1 flex-wrap items-center gap-2">
    <SearchInput
      :model-value="searchQuery"
      :placeholder="t('admin.accounts.searchAccounts')"
      class="w-full sm:w-72"
      @update:model-value="$emit('update:searchQuery', $event)"
      @search="$emit('change')"
    />
    <button
      type="button"
      class="filter-trigger"
      :class="activeCount > 0 && 'is-active'"
      :aria-expanded="showSheet"
      @click="showSheet = true"
    >
      <Icon name="filter" size="sm" />
      <span>{{ t('admin.accounts.openFilters') }}</span>
      <span v-if="activeCount > 0" class="filter-count-badge">{{ activeCount }}</span>
    </button>
    <button
      v-if="activeCount > 0"
      type="button"
      class="clear-filters-btn"
      :title="t('admin.accounts.clearAllFilters')"
      @click="clearAllFilters"
    >
      <Icon name="x" size="sm" />
      <span class="hidden lg:inline">{{ t('admin.accounts.clearAllFilters') }}</span>
    </button>
    <AccountFilterSheet
      :show="showSheet"
      :filters="filters"
      :groups="groups"
      @apply="onApply"
      @close="showSheet = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountFilterSheet from './AccountFilterSheet.vue'
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

const showSheet = ref(false)

const FILTER_KEYS: FilterKey[] = ['platform', 'type', 'status', 'privacy_mode', 'group']

const isActive = (key: FilterKey): boolean => {
  const value = props.filters?.[key]
  return value !== '' && value !== null && value !== undefined
}

const activeCount = computed(() => FILTER_KEYS.reduce((n, k) => n + (isActive(k) ? 1 : 0), 0))

const onApply = (next: Record<string, FilterValue>) => {
  emit('update:filters', next)
  emit('change')
}

const clearAllFilters = () => {
  const next: Record<string, FilterValue> = { ...props.filters }
  for (const k of FILTER_KEYS) next[k] = ''
  emit('update:filters', next)
  emit('change')
}
</script>

<style scoped>
.filter-trigger {
  @apply inline-flex items-center gap-1.5 rounded-xl border px-3 py-2.5 text-sm font-medium;
  @apply border-gray-200 bg-white text-gray-700 transition-all duration-200;
  @apply hover:border-gray-300 hover:bg-gray-50;
  @apply focus:outline-none focus:ring-2 focus:ring-primary-500/30;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200;
  @apply dark:hover:border-dark-500 dark:hover:bg-dark-700;
}

.filter-trigger.is-active {
  @apply border-primary-500/60 bg-primary-50/70 text-primary-700 ring-1 ring-primary-500/15;
  @apply dark:border-primary-400/50 dark:bg-primary-900/20 dark:text-primary-200 dark:ring-primary-400/15;
}

.filter-count-badge {
  @apply ml-0.5 inline-flex h-5 min-w-[20px] items-center justify-center rounded-full px-1.5;
  @apply bg-primary-500 text-xs font-semibold text-white;
  @apply dark:bg-primary-400 dark:text-primary-950;
}

.clear-filters-btn {
  @apply inline-flex items-center gap-1.5 rounded-xl border px-3 py-2.5 text-sm font-medium;
  @apply border-gray-200 bg-white text-gray-600 transition-all duration-200;
  @apply hover:border-red-300 hover:bg-red-50 hover:text-red-600;
  @apply focus:outline-none focus:ring-2 focus:ring-red-500/30;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300;
  @apply dark:hover:border-red-500/40 dark:hover:bg-red-500/10 dark:hover:text-red-300;
}
</style>
