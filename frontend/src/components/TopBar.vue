<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useAccountsStore } from '../stores/accounts'
import { useFilesStore } from '../stores/files'
import { formatBytes } from '../composables/useBytes'

const auth = useAuthStore()
const accounts = useAccountsStore()
const files = useFilesStore()
const router = useRouter()

// Same aggregation `rhino status` does client-side from ListAccountStatus —
// no separate backend endpoint needed for numbers already in these stores.
const totals = computed(() => {
  let healthy = 0
  let total = 0
  let available = 0
  for (const a of accounts.accounts) {
    if (a.error) continue
    healthy++
    if (!a.unlimited) {
      total += a.limit ?? 0
      available += a.available ?? 0
    }
  }
  return { healthy, count: accounts.accounts.length, total, available }
})

let searchTimer: ReturnType<typeof setTimeout> | undefined
function onSearchInput() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => files.refresh(), 250)
}

async function logout() {
  await auth.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <header class="topbar">
    <input
      v-model="files.search"
      class="topbar__search"
      type="search"
      placeholder="Search files"
      @input="onSearchInput"
    />

    <div class="topbar__totals">
      {{ totals.healthy }}/{{ totals.count }} drives healthy ·
      {{ formatBytes(totals.available) }} free of {{ formatBytes(totals.total) }}
    </div>

    <div class="topbar__user">
      <span>{{ auth.username }}</span>
      <button class="btn" @click="logout">Log out</button>
    </div>
  </header>
</template>

<style scoped>
.topbar {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--border);
}

.topbar__search {
  flex: 1;
  max-width: 360px;
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 0.45rem 1rem;
  background: var(--bg-alt);
  color: var(--text);
}

.topbar__totals {
  font-size: 0.8rem;
  color: var(--text-dim);
  white-space: nowrap;
}

.topbar__user {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  font-size: 0.9rem;
}
</style>
