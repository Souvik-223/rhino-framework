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

  async function addAccount(label: string) {
    await api.addAccount(label)
    await refresh()
  }

  async function removeAccount(label: string) {
    await api.removeAccount(label)
    await refresh()
  }

  return { accounts, loading, refresh, addAccount, removeAccount }
})
