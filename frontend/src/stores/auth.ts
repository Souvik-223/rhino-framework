import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, ApiError } from '../api/client'

export const useAuthStore = defineStore('auth', () => {
  const username = ref<string | null>(null)
  const checked = ref(false)

  async function checkSession() {
    try {
      const me = await api.me()
      username.value = me.username
    } catch {
      username.value = null
    } finally {
      checked.value = true
    }
  }

  async function login(user: string, password: string) {
    const res = await api.login(user, password)
    username.value = res.username
  }

  async function register(user: string, password: string) {
    const res = await api.register(user, password)
    username.value = res.username
  }

  async function logout() {
    await api.logout()
    username.value = null
  }

  return { username, checked, checkSession, login, register, logout }
})

export { ApiError }
