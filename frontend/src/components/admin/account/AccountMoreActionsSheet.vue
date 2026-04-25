<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.moreActions.title')"
    width="normal"
    :close-on-click-outside="true"
    @close="$emit('close')"
  >
    <ul class="divide-y divide-gray-100 dark:divide-dark-700">
      <!-- Manual refresh -->
      <li>
        <button type="button" class="action-row" :disabled="loading" @click="onRefresh">
          <Icon name="refresh" size="md" :class="['action-icon text-gray-500', loading ? 'animate-spin' : '']" />
          <span class="action-label">{{ t('common.refresh') }}</span>
        </button>
      </li>

      <!-- Auto refresh (expandable) -->
      <li>
        <button type="button" class="action-row" @click="toggleSection('autoRefresh')">
          <Icon name="clock" size="md" class="action-icon text-gray-500" />
          <span class="action-label">{{ t('admin.accounts.autoRefresh') }}</span>
          <span v-if="autoRefreshEnabled" class="action-meta text-primary-600 dark:text-primary-400">
            {{ t('admin.accounts.autoRefreshCountdown', { seconds: autoRefreshCountdown }) }}
          </span>
          <span v-else class="action-meta text-gray-400 dark:text-dark-500">
            {{ t('admin.accounts.moreActions.autoRefreshOff') }}
          </span>
          <Icon
            name="chevronDown"
            size="sm"
            class="text-gray-400 transition-transform"
            :class="{ 'rotate-180': expanded === 'autoRefresh' }"
          />
        </button>
        <div v-if="expanded === 'autoRefresh'" class="bg-gray-50 px-4 pb-3 dark:bg-dark-900/40">
          <label class="flex items-center justify-between rounded-lg bg-white px-3 py-2.5 dark:bg-dark-800">
            <span class="text-sm text-gray-700 dark:text-gray-200">
              {{ t('admin.accounts.enableAutoRefresh') }}
            </span>
            <button
              type="button"
              role="switch"
              :aria-checked="autoRefreshEnabled"
              class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:focus:ring-offset-dark-800"
              :class="autoRefreshEnabled ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-600'"
              @click="$emit('toggle-auto-refresh', !autoRefreshEnabled)"
            >
              <span
                class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out"
                :class="autoRefreshEnabled ? 'translate-x-4' : 'translate-x-0'"
              />
            </button>
          </label>
          <div class="mt-2 grid grid-cols-2 gap-2">
            <button
              v-for="sec in autoRefreshIntervals"
              :key="sec"
              type="button"
              class="rounded-lg border px-3 py-2 text-sm font-medium transition-colors"
              :class="
                autoRefreshIntervalSeconds === sec
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-200'
                  : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-100 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700'
              "
              @click="$emit('set-auto-refresh-interval', sec)"
            >
              {{ formatInterval(sec) }}
            </button>
          </div>
        </div>
      </li>

      <!-- Sync from CRS -->
      <li>
        <button type="button" class="action-row" @click="onAction('sync')">
          <Icon name="sync" size="md" class="action-icon text-blue-500" />
          <span class="action-label">{{ t('admin.accounts.syncFromCrs') }}</span>
        </button>
      </li>

      <!-- Data import -->
      <li>
        <button type="button" class="action-row" @click="onAction('import')">
          <Icon name="upload" size="md" class="action-icon text-emerald-500" />
          <span class="action-label">{{ t('admin.accounts.dataImport') }}</span>
        </button>
      </li>

      <!-- Data export -->
      <li>
        <button type="button" class="action-row" @click="onAction('export')">
          <Icon name="download" size="md" class="action-icon text-amber-500" />
          <span class="action-label">
            {{ selectedCount > 0 ? t('admin.accounts.dataExportSelected') : t('admin.accounts.dataExport') }}
          </span>
          <span v-if="selectedCount > 0" class="action-meta text-amber-600 dark:text-amber-400">
            {{ selectedCount }}
          </span>
        </button>
      </li>

      <!-- Error passthrough rules -->
      <li>
        <button type="button" class="action-row" @click="onAction('error-passthrough')">
          <Icon name="shield" size="md" class="action-icon text-rose-500" />
          <span class="action-label">{{ t('admin.errorPassthrough.title') }}</span>
        </button>
      </li>

      <!-- TLS fingerprint -->
      <li>
        <button type="button" class="action-row" @click="onAction('tls-fingerprint')">
          <Icon name="lock" size="md" class="action-icon text-indigo-500" />
          <span class="action-label">{{ t('admin.tlsFingerprintProfiles.title') }}</span>
        </button>
      </li>

      <!-- Column settings (expandable) -->
      <li>
        <button type="button" class="action-row" @click="toggleSection('columns')">
          <Icon name="grid" size="md" class="action-icon text-gray-500" />
          <span class="action-label">{{ t('admin.users.columnSettings') }}</span>
          <Icon
            name="chevronDown"
            size="sm"
            class="text-gray-400 transition-transform"
            :class="{ 'rotate-180': expanded === 'columns' }"
          />
        </button>
        <div v-if="expanded === 'columns'" class="bg-gray-50 px-4 pb-3 dark:bg-dark-900/40">
          <ul class="space-y-1">
            <li v-for="col in toggleableColumns" :key="col.key">
              <button
                type="button"
                class="flex w-full items-center justify-between rounded-lg bg-white px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700"
                @click="$emit('toggle-column', col.key)"
              >
                <span>{{ col.label }}</span>
                <Icon v-if="isColumnVisible(col.key)" name="check" size="sm" class="text-primary-500" />
              </button>
            </li>
          </ul>
        </div>
      </li>
    </ul>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

interface ColumnOption {
  key: string
  label: string
}

const props = defineProps<{
  show: boolean
  loading?: boolean
  autoRefreshEnabled: boolean
  autoRefreshIntervalSeconds: number
  autoRefreshCountdown: number
  autoRefreshIntervals: readonly number[]
  toggleableColumns: ColumnOption[]
  isColumnVisible: (key: string) => boolean
  selectedCount: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'refresh'): void
  (e: 'toggle-auto-refresh', enabled: boolean): void
  (e: 'set-auto-refresh-interval', seconds: number): void
  (e: 'toggle-column', key: string): void
  (e: 'sync'): void
  (e: 'import'): void
  (e: 'export'): void
  (e: 'error-passthrough'): void
  (e: 'tls-fingerprint'): void
}>()

const { t } = useI18n()

type ExpandableSection = 'autoRefresh' | 'columns' | null
const expanded = ref<ExpandableSection>(null)

const toggleSection = (section: Exclude<ExpandableSection, null>) => {
  expanded.value = expanded.value === section ? null : section
}

watch(
  () => props.show,
  visible => {
    if (!visible) expanded.value = null
  }
)

const onRefresh = () => {
  if (props.loading) return
  emit('refresh')
  emit('close')
}

type FireAndCloseAction =
  | 'sync'
  | 'import'
  | 'export'
  | 'error-passthrough'
  | 'tls-fingerprint'

const onAction = (action: FireAndCloseAction) => {
  switch (action) {
    case 'sync':
      emit('sync')
      break
    case 'import':
      emit('import')
      break
    case 'export':
      emit('export')
      break
    case 'error-passthrough':
      emit('error-passthrough')
      break
    case 'tls-fingerprint':
      emit('tls-fingerprint')
      break
  }
  emit('close')
}

const formatInterval = (seconds: number) => {
  const mapping: Record<number, string> = {
    5: t('admin.accounts.refreshInterval5s'),
    10: t('admin.accounts.refreshInterval10s'),
    15: t('admin.accounts.refreshInterval15s'),
    30: t('admin.accounts.refreshInterval30s')
  }
  return mapping[seconds] ?? `${seconds}s`
}
</script>

<style scoped>
.action-row {
  @apply flex w-full items-center gap-3 px-4 py-3.5 text-left;
  @apply text-gray-900 dark:text-gray-100;
  @apply transition-colors;
  @apply hover:bg-gray-50 active:bg-gray-100 dark:hover:bg-dark-700/60 dark:active:bg-dark-700;
  @apply disabled:cursor-not-allowed disabled:opacity-50;
}
.action-icon {
  @apply h-5 w-5 flex-shrink-0;
}
.action-label {
  @apply flex-1 text-sm font-medium;
}
.action-meta {
  @apply text-xs font-medium;
}
</style>
