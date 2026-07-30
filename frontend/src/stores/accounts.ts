import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type AccountStatus } from '../api/client'

export const useAccountsStore = defineStore('accounts', () => {
  const accounts = ref<AccountStatus[]>([])
  const loading = ref(false)

  async function refresh() {
    loading.value = true
    try {
      accounts.value = await api.listAccounts()
    } finally {
      loading.value = false
    }
  }

  // Connecting an account means leaving the SPA entirely for Google's real
  // consent screen, so this is a browser navigation, not an async action
  // with a resolved/rejected outcome — see api.connectAccountUrl.
  function connectAccount(label: string) {
    window.location.href = api.connectAccountUrl(label)
  }

  async function removeAccount(label: string) {
    await api.removeAccount(label)
    await refresh()
  }

  return { accounts, loading, refresh, connectAccount, removeAccount }
})
