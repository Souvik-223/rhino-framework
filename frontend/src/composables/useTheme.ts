import { ref } from 'vue'

type Theme = 'light' | 'dark'

const STORAGE_KEY = 'rhino-theme'

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function resolveInitialTheme(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'light' || stored === 'dark') return stored
  return systemPrefersDark() ? 'dark' : 'light'
}

const theme = ref<Theme>(resolveInitialTheme())

function apply(t: Theme) {
  document.documentElement.classList.toggle('dark', t === 'dark')
}

apply(theme.value)

export function useTheme() {
  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem(STORAGE_KEY, theme.value)
    apply(theme.value)
  }

  return { theme, toggle }
}
