<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAccountsStore } from '../stores/accounts'
import { formatBytes, usageLevel } from '../composables/useBytes'
import { Progress } from '@/components/ui/progress'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { HardDrive, Plus, X } from '@lucide/vue'

const store = useAccountsStore()
const newLabel = ref('')

onMounted(() => store.refresh())

function usedFraction(a: { limit?: number; usage?: number; unlimited?: boolean }) {
  if (a.unlimited || !a.limit) return 0
  return (a.usage ?? 0) / a.limit
}

const levelClasses: Record<'ok' | 'warn' | 'danger', string> = {
  ok: 'bg-ok',
  warn: 'bg-warn',
  danger: 'bg-destructive',
}

// Navigates the whole browser to Google's consent screen — see
// stores/accounts.ts and backend/handlers_oauth.go.
function connectAccount() {
  if (!newLabel.value.trim()) return
  store.connectAccount(newLabel.value.trim())
}
</script>

<template>
  <aside
    class="bg-sidebar border-sidebar-border flex w-72 shrink-0 flex-col gap-5 border-r p-5"
  >
    <div class="font-heading text-primary text-xl font-bold tracking-tight uppercase">Rhino</div>

    <div class="flex items-center justify-between">
      <h2 class="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
        Connected drives
      </h2>
    </div>

    <ul class="flex flex-col gap-3">
      <li
        v-for="(a, i) in store.accounts"
        :key="a.label"
        class="animate-fade-in-up bg-card/60 group rounded-lg border border-border/60 p-3 transition-colors hover:border-border"
        :style="{ animationDelay: `${i * 60}ms` }"
      >
        <div class="flex items-center justify-between gap-2">
          <div class="flex min-w-0 items-center gap-2">
            <HardDrive class="text-primary size-4 shrink-0" />
            <span class="truncate text-sm font-medium">{{ a.label }}</span>
          </div>
          <button
            class="text-muted-foreground hover:text-destructive shrink-0 opacity-0 transition-opacity group-hover:opacity-100"
            title="Disconnect"
            @click="store.removeAccount(a.label)"
          >
            <X class="size-3.5" />
          </button>
        </div>

        <p v-if="a.error" class="text-destructive mt-1.5 text-xs">unavailable: {{ a.error }}</p>
        <p v-else-if="a.unlimited" class="text-muted-foreground mt-1.5 text-xs">unlimited</p>
        <template v-else>
          <Progress
            :model-value="Math.min(usedFraction(a) * 100, 100)"
            class="mt-2 h-1.5"
            :indicator-class="levelClasses[usageLevel(usedFraction(a))]"
          />
          <p class="text-muted-foreground mt-1.5 font-mono text-[11px]">
            {{ formatBytes(a.usage ?? 0) }} / {{ formatBytes(a.limit ?? 0) }}
          </p>
        </template>
      </li>
    </ul>

    <p v-if="!store.loading && store.accounts.length === 0" class="text-muted-foreground text-sm">
      No drives connected yet.
    </p>

    <form class="mt-auto flex flex-col gap-2 border-t border-border/60 pt-4" @submit.prevent="connectAccount">
      <Input v-model="newLabel" placeholder="e.g. home-gmail" class="h-9" />
      <Button type="submit" class="w-full gap-1.5">
        <Plus class="size-4" />
        Connect account
      </Button>
    </form>
  </aside>
</template>
