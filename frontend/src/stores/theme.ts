/**
 * Theme Store
 * Manages tri-state theme (light / dark / system). When in `system` mode,
 * follows OS preference in real time via matchMedia.
 */

import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'theme'
const LIGHT_THEME_COLOR = '#f8fafc'
const DARK_THEME_COLOR = '#0f172a'

function readSavedMode(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'light' || saved === 'dark' || saved === 'system') {
    return saved
  }
  return 'system'
}

function getSystemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function applyThemeToDocument(isDark: boolean) {
  document.documentElement.classList.toggle('dark', isDark)

  // Keep status-bar / theme-color meta in sync with the resolved theme.
  // We use a JS-controlled (non-media) meta so it overrides the static
  // media-keyed metas in index.html when the user picks an explicit mode.
  let meta = document.querySelector<HTMLMetaElement>(
    'meta[name="theme-color"]:not([media])'
  )
  if (!meta) {
    meta = document.createElement('meta')
    meta.name = 'theme-color'
    document.head.appendChild(meta)
  }
  meta.content = isDark ? DARK_THEME_COLOR : LIGHT_THEME_COLOR
}

export const useThemeStore = defineStore('theme', () => {
  const mode = ref<ThemeMode>(readSavedMode())
  const systemPrefersDark = ref<boolean>(getSystemPrefersDark())

  const isDark = computed<boolean>(() =>
    mode.value === 'system' ? systemPrefersDark.value : mode.value === 'dark'
  )

  let mediaQuery: MediaQueryList | null = null
  let mediaListener: ((e: MediaQueryListEvent) => void) | null = null

  function init() {
    applyThemeToDocument(isDark.value)

    if (mediaQuery) return
    mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    mediaListener = (event) => {
      systemPrefersDark.value = event.matches
    }
    mediaQuery.addEventListener('change', mediaListener)
  }

  function dispose() {
    if (mediaQuery && mediaListener) {
      mediaQuery.removeEventListener('change', mediaListener)
    }
    mediaQuery = null
    mediaListener = null
  }

  function setMode(next: ThemeMode) {
    mode.value = next
    localStorage.setItem(STORAGE_KEY, next)
  }

  function cycle() {
    const order: ThemeMode[] = ['light', 'dark', 'system']
    const nextIndex = (order.indexOf(mode.value) + 1) % order.length
    setMode(order[nextIndex])
  }

  watch(isDark, applyThemeToDocument)

  return {
    mode,
    isDark,
    init,
    dispose,
    setMode,
    cycle,
  }
})
