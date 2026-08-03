import { defineStore } from 'pinia'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { api, ApiError, type AccountStatus } from '../api/client'

export const useAccountsStore = defineStore('accounts', () => {
  const accounts = ref<AccountStatus[]>([])
  const loading = ref(false)

  async function refresh() {
    loading.value = true
    try {
      accounts.value = await api.listAccounts()
    } catch (err) {
      toast.error('Failed to load accounts', {
        description: err instanceof Error ? err.message : 'unknown error',
      })
    } finally {
      loading.value = false
    }
  }

  function connectAccount(label: string) {
    window.location.href = api.connectAccountUrl(label)
  }

  async function removeAccount(label: string, force = false) {
    try {
      await api.removeAccount(label, force)
      toast.success(`Disconnected "${label}"`)
      await refresh()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(`"${label}" still holds file data`, {
          description: `${err.message} Disconnecting anyway makes that data unrecoverable unless this same drive is reconnected later.`,
          action: { label: 'Disconnect anyway', onClick: () => removeAccount(label, true) },
        })
        return
      }
      toast.error(`Failed to disconnect "${label}"`, {
        description: err instanceof Error ? err.message : 'unknown error',
      })
    }
  }

  return { accounts, loading, refresh, connectAccount, removeAccount }
})
