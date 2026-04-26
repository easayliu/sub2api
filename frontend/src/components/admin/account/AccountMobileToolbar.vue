<template>
  <div class="space-y-2">
    <!-- Sticky main toolbar row -->
    <div class="sticky top-0 z-30 -mx-4 -mt-4 border-b border-gray-200 bg-white/95 px-4 pb-2 pt-3 backdrop-blur-md dark:border-dark-700 dark:bg-dark-900/95">
      <!-- Multi-select mode -->
      <div v-if="multiSelectMode" class="flex items-center gap-2">
        <button
          type="button"
          class="-ml-1 flex h-9 w-9 items-center justify-center rounded-lg text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
          :aria-label="t('admin.accounts.exitMultiSelect')"
          @click="$emit('clear-selection')"
        >
          <Icon name="x" size="md" />
        </button>
        <div class="flex min-w-0 flex-1 flex-col">
          <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.accounts.bulkActions.selected', { count: selectedCount }) }}
          </span>
          <button
            type="button"
            class="self-start text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
            @click="$emit('select-page')"
          >
            {{ t('admin.accounts.bulkActions.selectCurrentPage') }}
          </button>
        </div>
        <button type="button" class="toolbar-icon-btn" :aria-label="t('admin.accounts.moreActions.bulkActions')" @click="showBulkSheet = true">
          <Icon name="more" size="md" />
        </button>
      </div>

      <!-- Default mode -->
      <div v-else class="flex items-center gap-2">
        <SearchInput
          :model-value="searchQuery"
          :placeholder="t('admin.accounts.searchAccounts')"
          class="min-w-0 flex-1"
          @update:model-value="$emit('update:searchQuery', $event)"
          @search="$emit('search-change')"
        />
        <button type="button" class="toolbar-icon-btn relative" :aria-label="t('admin.accounts.openFilters')" @click="showFilterSheet = true">
          <Icon name="filter" size="md" />
          <span
            v-if="activeFilterCount > 0"
            class="absolute -right-1 -top-1 flex h-4 min-w-[16px] items-center justify-center rounded-full bg-primary-500 px-1 text-[10px] font-semibold text-white"
          >
            {{ activeFilterCount }}
          </span>
        </button>
        <button
          type="button"
          class="toolbar-icon-btn relative"
          :aria-label="t('common.refresh')"
          :disabled="loading"
          @click="$emit('refresh')"
        >
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          <span
            v-if="autoRefreshEnabled"
            class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-primary-500 ring-2 ring-white dark:ring-dark-900"
            :aria-label="t('admin.accounts.autoRefresh')"
          />
        </button>
        <button type="button" class="toolbar-icon-btn" :aria-label="t('common.more')" @click="showMoreSheet = true">
          <Icon name="more" size="md" />
        </button>
        <button
          type="button"
          class="flex h-9 items-center gap-1 rounded-lg bg-primary-500 px-3 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-primary-600 active:bg-primary-700"
          @click="$emit('create')"
        >
          <Icon name="plus" size="sm" :stroke-width="2.5" />
          <span>{{ t('common.add') }}</span>
        </button>
      </div>
    </div>

    <!-- Active filter chips -->
    <div
      v-if="!multiSelectMode && activeFilterCount > 0"
      class="-mx-1 flex items-center gap-2 overflow-x-auto px-1 pb-1 scrollbar-hide"
    >
      <button
        v-for="chip in activeChips"
        :key="chip.key"
        type="button"
        class="inline-flex flex-shrink-0 items-center gap-1 rounded-full border border-primary-200 bg-primary-50 px-3 py-1 text-xs font-medium text-primary-700 transition-colors hover:bg-primary-100 dark:border-primary-700/50 dark:bg-primary-900/30 dark:text-primary-200 dark:hover:bg-primary-900/40"
        @click="clearFilter(chip.key)"
      >
        <span class="opacity-70">{{ chip.section }}</span>
        <span class="truncate max-w-[120px]">{{ chip.label }}</span>
        <Icon name="x" size="xs" :stroke-width="2.25" class="opacity-70" />
      </button>
      <button
        type="button"
        class="ml-auto flex-shrink-0 text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
        @click="clearAllFilters"
      >
        {{ t('admin.accounts.clearAllFilters') }}
      </button>
    </div>

    <!-- Pending sync notice -->
    <div
      v-if="hasPendingListSync && !multiSelectMode"
      class="flex items-center justify-between rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-700/40 dark:bg-amber-900/20 dark:text-amber-200"
    >
      <span class="flex-1 truncate">{{ t('admin.accounts.listPendingSyncHint') }}</span>
      <button class="ml-2 shrink-0 text-xs font-semibold underline" @click="$emit('sync-pending-list')">
        {{ t('admin.accounts.listPendingSyncAction') }}
      </button>
    </div>

    <!-- Filter sheet -->
    <AccountFilterSheet
      :show="showFilterSheet"
      :filters="filters"
      :groups="groups"
      @close="showFilterSheet = false"
      @apply="onApplyFilters"
    />

    <!-- More actions sheet -->
    <AccountMoreActionsSheet
      :show="showMoreSheet"
      :auto-refresh-enabled="autoRefreshEnabled"
      :auto-refresh-interval-seconds="autoRefreshIntervalSeconds"
      :auto-refresh-countdown="autoRefreshCountdown"
      :auto-refresh-intervals="autoRefreshIntervals"
      :toggleable-columns="toggleableColumns"
      :is-column-visible="isColumnVisible"
      :selected-count="selectedCount"
      @close="showMoreSheet = false"
      @toggle-auto-refresh="$emit('toggle-auto-refresh', $event)"
      @set-auto-refresh-interval="$emit('set-auto-refresh-interval', $event)"
      @toggle-column="$emit('toggle-column', $event)"
      @sync="$emit('sync')"
      @import="$emit('import')"
      @export="$emit('export')"
      @error-passthrough="$emit('error-passthrough')"
      @tls-fingerprint="$emit('tls-fingerprint')"
    />

    <!-- Bulk actions sheet (multi-select mode) -->
    <BaseDialog
      :show="showBulkSheet"
      :title="t('admin.accounts.moreActions.bulkActions')"
      width="normal"
      :close-on-click-outside="true"
      @close="showBulkSheet = false"
    >
      <ul class="divide-y divide-gray-100 dark:divide-dark-700">
        <li>
          <button type="button" class="bulk-row text-primary-600 dark:text-primary-400" @click="onBulk('edit')">
            <Icon name="edit" size="md" class="bulk-icon" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.edit') }}</span>
          </button>
        </li>
        <li>
          <button type="button" class="bulk-row text-emerald-600 dark:text-emerald-400" @click="onBulk('toggle-schedulable-on')">
            <Icon name="play" size="md" class="bulk-icon" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.enableScheduling') }}</span>
          </button>
        </li>
        <li>
          <button type="button" class="bulk-row text-amber-600 dark:text-amber-400" @click="onBulk('toggle-schedulable-off')">
            <Icon name="x" size="md" class="bulk-icon" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.disableScheduling') }}</span>
          </button>
        </li>
        <li>
          <button type="button" class="bulk-row" @click="onBulk('reset-status')">
            <Icon name="sync" size="md" class="bulk-icon text-blue-500" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.resetStatus') }}</span>
          </button>
        </li>
        <li>
          <button type="button" class="bulk-row" @click="onBulk('refresh-token')">
            <Icon name="refresh" size="md" class="bulk-icon text-purple-500" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.refreshToken') }}</span>
          </button>
        </li>
        <li>
          <button type="button" class="bulk-row text-red-600 dark:text-red-400" @click="onBulk('delete')">
            <Icon name="trash" size="md" class="bulk-icon" />
            <span class="bulk-label">{{ t('admin.accounts.bulkActions.delete') }}</span>
          </button>
        </li>
      </ul>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SearchInput from '@/components/common/SearchInput.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountFilterSheet from './AccountFilterSheet.vue'
import AccountMoreActionsSheet from './AccountMoreActionsSheet.vue'
import type { AdminGroup } from '@/types'

type FilterValue = string | number | boolean | null
type FilterKey = 'platform' | 'type' | 'status' | 'privacy_mode' | 'group'

interface ColumnOption {
  key: string
  label: string
}

const props = defineProps<{
  searchQuery: string
  filters: Record<string, FilterValue>
  groups?: AdminGroup[]
  selectedCount: number
  hasPendingListSync?: boolean
  loading?: boolean
  autoRefreshEnabled: boolean
  autoRefreshIntervalSeconds: number
  autoRefreshCountdown: number
  autoRefreshIntervals: readonly number[]
  toggleableColumns: ColumnOption[]
  isColumnVisible: (key: string) => boolean
}>()

const emit = defineEmits<{
  (e: 'update:searchQuery', value: string): void
  (e: 'update:filters', filters: Record<string, FilterValue>): void
  (e: 'search-change'): void
  (e: 'filter-change'): void
  (e: 'create'): void
  (e: 'refresh'): void
  (e: 'sync'): void
  (e: 'import'): void
  (e: 'export'): void
  (e: 'error-passthrough'): void
  (e: 'tls-fingerprint'): void
  (e: 'toggle-auto-refresh', enabled: boolean): void
  (e: 'set-auto-refresh-interval', seconds: number): void
  (e: 'toggle-column', key: string): void
  (e: 'clear-selection'): void
  (e: 'select-page'): void
  (e: 'sync-pending-list'): void
  (e: 'bulk-edit'): void
  (e: 'bulk-delete'): void
  (e: 'bulk-reset-status'): void
  (e: 'bulk-refresh-token'): void
  (e: 'bulk-toggle-schedulable', enabled: boolean): void
}>()

const { t } = useI18n()

const showFilterSheet = ref(false)
const showMoreSheet = ref(false)
const showBulkSheet = ref(false)

const multiSelectMode = computed(() => props.selectedCount > 0)

const FILTER_KEYS: FilterKey[] = ['platform', 'type', 'status', 'privacy_mode', 'group']

const activeFilterCount = computed(() => FILTER_KEYS.filter(k => !!props.filters?.[k]).length)

interface ChipDescriptor {
  key: FilterKey
  section: string
  label: string
}

const platformLabel = (value: string) => {
  const map: Record<string, string> = {
    anthropic: 'Anthropic',
    openai: 'OpenAI',
    gemini: 'Gemini',
    antigravity: 'Antigravity'
  }
  return map[value] ?? value
}

const typeLabel = (value: string) => {
  const map: Record<string, string> = {
    oauth: t('admin.accounts.oauthType'),
    'setup-token': t('admin.accounts.setupToken'),
    apikey: t('admin.accounts.apiKey'),
    bedrock: 'AWS Bedrock'
  }
  return map[value] ?? value
}

const statusLabel = (value: string) => {
  const map: Record<string, string> = {
    active: t('admin.accounts.status.active'),
    inactive: t('admin.accounts.status.inactive'),
    error: t('admin.accounts.status.error'),
    rate_limited: t('admin.accounts.status.rateLimited'),
    temp_unschedulable: t('admin.accounts.status.tempUnschedulable'),
    unschedulable: t('admin.accounts.status.unschedulable')
  }
  return map[value] ?? value
}

const privacyLabel = (value: string) => {
  const map: Record<string, string> = {
    __unset__: t('admin.accounts.privacyUnset'),
    training_off: 'Privacy',
    training_set_cf_blocked: 'CF',
    training_set_failed: 'Fail'
  }
  return map[value] ?? value
}

const groupLabel = (value: string) => {
  if (value === 'ungrouped') return t('admin.accounts.ungroupedGroup')
  const found = (props.groups ?? []).find(g => String(g.id) === value)
  return found?.name ?? value
}

const activeChips = computed<ChipDescriptor[]>(() => {
  const chips: ChipDescriptor[] = []
  for (const k of FILTER_KEYS) {
    const raw = props.filters?.[k]
    if (raw === '' || raw === null || raw === undefined) continue
    const value = String(raw)
    switch (k) {
      case 'platform':
        chips.push({ key: k, section: t('admin.accounts.filterSheet.platform'), label: platformLabel(value) })
        break
      case 'type':
        chips.push({ key: k, section: t('admin.accounts.filterSheet.type'), label: typeLabel(value) })
        break
      case 'status':
        chips.push({ key: k, section: t('admin.accounts.filterSheet.status'), label: statusLabel(value) })
        break
      case 'privacy_mode':
        chips.push({ key: k, section: t('admin.accounts.filterSheet.privacy'), label: privacyLabel(value) })
        break
      case 'group':
        chips.push({ key: k, section: t('admin.accounts.filterSheet.group'), label: groupLabel(value) })
        break
    }
  }
  return chips
})

const onApplyFilters = (next: Record<string, FilterValue>) => {
  emit('update:filters', next)
  emit('filter-change')
}

const clearFilter = (key: FilterKey) => {
  const next: Record<string, FilterValue> = { ...props.filters, [key]: '' }
  emit('update:filters', next)
  emit('filter-change')
}

const clearAllFilters = () => {
  const next: Record<string, FilterValue> = { ...props.filters }
  for (const k of FILTER_KEYS) next[k] = ''
  emit('update:filters', next)
  emit('filter-change')
}

type BulkAction =
  | 'edit'
  | 'delete'
  | 'reset-status'
  | 'refresh-token'
  | 'toggle-schedulable-on'
  | 'toggle-schedulable-off'

const onBulk = (action: BulkAction) => {
  switch (action) {
    case 'edit':
      emit('bulk-edit')
      break
    case 'delete':
      emit('bulk-delete')
      break
    case 'reset-status':
      emit('bulk-reset-status')
      break
    case 'refresh-token':
      emit('bulk-refresh-token')
      break
    case 'toggle-schedulable-on':
      emit('bulk-toggle-schedulable', true)
      break
    case 'toggle-schedulable-off':
      emit('bulk-toggle-schedulable', false)
      break
  }
  showBulkSheet.value = false
}
</script>

<style scoped>
.toolbar-icon-btn {
  @apply relative flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg;
  @apply border border-gray-200 bg-white text-gray-600;
  @apply transition-colors hover:bg-gray-50 active:bg-gray-100;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700;
}
.bulk-row {
  @apply flex w-full items-center gap-3 px-4 py-3.5 text-left;
  @apply transition-colors;
  @apply hover:bg-gray-50 active:bg-gray-100 dark:hover:bg-dark-700/60 dark:active:bg-dark-700;
}
.bulk-icon {
  @apply h-5 w-5 flex-shrink-0;
}
.bulk-label {
  @apply text-sm font-medium;
}
</style>
