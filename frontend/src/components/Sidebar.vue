<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAccountsStore } from '../stores/accounts'
import { useSidebar } from '../composables/useSidebar'
import { formatBytes, usageLevel } from '../composables/useBytes'
import { Progress } from '@/components/ui/progress'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import logoUrl from '../assets/logo.png'
import { HardDrive, Plus, X, PanelLeftClose } from '@lucide/vue'

const store = useAccountsStore()
const newLabel = ref('')
const { collapsed, toggle: toggleSidebar } = useSidebar()

onMounted(() => store.refresh())

const healthyCount = computed(() => store.accounts.filter((a) => !a.error).length)

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
  // The label input is hidden while collapsed, so submitting here just
  // reveals it instead of firing a request with an empty label.
  if (collapsed.value) {
    collapsed.value = false
    return
  }
  if (!newLabel.value.trim()) return
  store.connectAccount(newLabel.value.trim())
}
</script>

<template>
  <aside
    :class="[
      'bg-sidebar border-sidebar-border flex shrink-0 flex-col border-r transition-[width] duration-200 ease-out',
      collapsed ? 'w-19 items-start gap-4 p-3' : 'w-72 gap-5 p-5',
    ]"
  >
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-2.5">
        <button v-if="collapsed" class="shrink-0" title="Expand sidebar" @click="toggleSidebar">
          <img :src="logoUrl" alt="Rhino" class="h-7 w-auto object-contain" />
        </button>
        <img v-else :src="logoUrl" alt="Rhino" class="h-7 w-auto shrink-0 object-contain" />
        <div v-if="!collapsed" class="flex flex-col leading-none">
          <span class="font-heading text-foreground text-base font-bold tracking-tight uppercase">Rhino</span>
          <span class="font-heading text-muted-foreground text-[9px] font-semibold tracking-[0.2em] uppercase">
            Storage
          </span>
        </div>
      </div>

      <button
        v-if="!collapsed"
        class="border-border bg-card text-muted-foreground hover:text-foreground flex size-6 shrink-0 items-center justify-center rounded-full border shadow-sm transition-colors"
        title="Collapse sidebar"
        @click="toggleSidebar"
      >
        <PanelLeftClose class="size-3.5" />
      </button>
    </div>

    <div v-if="!collapsed" class="flex items-center justify-between">
      <h2 class="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
        Connected drives
      </h2>
      <span class="text-muted-foreground font-mono text-[11px]">{{ healthyCount }}/{{ store.accounts.length }}</span>
    </div>

    <ul :class="['flex flex-col', collapsed ? 'items-start gap-2' : 'gap-3']">
      <li
        v-for="(a, i) in store.accounts"
        :key="a.label"
        :title="collapsed ? a.label : undefined"
        class="animate-fade-in-up bg-card/60 group rounded-lg border border-border/60 transition-colors hover:border-border"
        :class="collapsed ? 'flex size-10 items-center justify-center' : 'p-3'"
        :style="{ animationDelay: `${i * 60}ms` }"
      >
        <HardDrive v-if="collapsed" class="text-primary size-4 shrink-0" />
        <template v-else>
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
        </template>
      </li>
    </ul>

    <p v-if="!collapsed && !store.loading && store.accounts.length === 0" class="text-muted-foreground text-sm">
      No drives connected yet.
    </p>

    <form
      :class="[
        'mt-auto flex border-t pt-4',
        collapsed ? 'border-sidebar-border' : 'border-border/60 flex-col gap-2',
      ]"
      @submit.prevent="connectAccount"
    >
      <Input v-if="!collapsed" v-model="newLabel" placeholder="e.g. home-gmail" class="h-8" />
      <Button
        type="submit"
        :size="collapsed ? 'icon-sm' : 'sm'"
        :class="!collapsed && 'w-full gap-1.5'"
        :title="collapsed ? 'Connect account' : undefined"
      >
        <Plus class="size-3.5" />
        <span v-if="!collapsed">Connect account</span>
      </Button>
    </form>
  </aside>
</template>
