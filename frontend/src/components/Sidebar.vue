<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAccountsStore } from '../stores/accounts'
import { formatBytes, usageLevel } from '../composables/useBytes'

const store = useAccountsStore()
const newLabel = ref('')
const adding = ref(false)
const addError = ref('')

onMounted(() => store.refresh())

function usedFraction(a: { limit?: number; usage?: number; unlimited?: boolean }) {
  if (a.unlimited || !a.limit) return 0
  return (a.usage ?? 0) / a.limit
}

async function connectAccount() {
  if (!newLabel.value.trim()) return
  adding.value = true
  addError.value = ''
  try {
    await store.addAccount(newLabel.value.trim())
    newLabel.value = ''
  } catch (err) {
    addError.value = err instanceof Error ? err.message : 'could not connect account'
  } finally {
    adding.value = false
  }
}
</script>

<template>
  <aside class="sidebar">
    <h2 class="sidebar__title">Connected drives</h2>

    <ul class="sidebar__accounts">
      <li v-for="a in store.accounts" :key="a.label" class="account">
        <div class="account__row">
          <span class="account__label">{{ a.label }}</span>
          <button
            class="account__remove"
            title="Disconnect"
            @click="store.removeAccount(a.label)"
          >
            &times;
          </button>
        </div>

        <p v-if="a.error" class="account__error">unavailable: {{ a.error }}</p>
        <p v-else-if="a.unlimited" class="account__meta">unlimited</p>
        <template v-else>
          <div class="usage-bar">
            <div
              class="usage-bar__fill"
              :class="`usage-bar__fill--${usageLevel(usedFraction(a))}`"
              :style="{ width: `${Math.min(usedFraction(a) * 100, 100)}%` }"
            />
          </div>
          <p class="account__meta">
            {{ formatBytes(a.usage ?? 0) }} of {{ formatBytes(a.limit ?? 0) }} used
          </p>
        </template>
      </li>
    </ul>

    <p v-if="!store.loading && store.accounts.length === 0" class="sidebar__empty">
      No drives connected yet.
    </p>

    <form class="sidebar__connect" @submit.prevent="connectAccount">
      <input v-model="newLabel" placeholder="e.g. home-gmail" :disabled="adding" />
      <button class="btn btn-primary" type="submit" :disabled="adding">
        + Connect account
      </button>
      <p v-if="addError" class="account__error">{{ addError }}</p>
    </form>
  </aside>
</template>

<style scoped>
.sidebar {
  width: 260px;
  flex-shrink: 0;
  border-right: 1px solid var(--border);
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
  overflow-y: auto;
}

.sidebar__title {
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-dim);
  margin: 0;
}

.sidebar__accounts {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.account__row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.account__label {
  font-weight: 600;
  font-size: 0.9rem;
}

.account__remove {
  border: none;
  background: none;
  color: var(--text-dim);
  font-size: 1rem;
  line-height: 1;
}

.account__remove:hover {
  color: var(--danger);
}

.account__meta {
  margin: 0.25rem 0 0;
  font-size: 0.78rem;
  color: var(--text-dim);
}

.account__error {
  margin: 0.25rem 0 0;
  font-size: 0.78rem;
  color: var(--danger);
}

.sidebar__empty {
  font-size: 0.85rem;
  color: var(--text-dim);
}

.sidebar__connect {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding-top: 1rem;
  border-top: 1px solid var(--border);
}

.sidebar__connect input {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.4rem 0.6rem;
  background: var(--bg);
  color: var(--text);
}
</style>
