import { ref } from 'vue'

const STORAGE_KEY = 'rhino-sidebar-collapsed'

function resolveInitial(): boolean {
  return localStorage.getItem(STORAGE_KEY) === '1'
}

const collapsed = ref(resolveInitial())

export function useSidebar() {
  function toggle() {
    collapsed.value = !collapsed.value
    localStorage.setItem(STORAGE_KEY, collapsed.value ? '1' : '0')
  }

  return { collapsed, toggle }
}
