<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useAccountsStore } from '../stores/accounts'
import { useFilesStore } from '../stores/files'
import { useTheme } from '../composables/useTheme'
import { formatBytes } from '../composables/useBytes'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Search, Moon, Sun, LogOut, ChevronDown } from '@lucide/vue'

const auth = useAuthStore()
const accounts = useAccountsStore()
const files = useFilesStore()
const router = useRouter()
const { theme, toggle } = useTheme()

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

const initials = computed(() => (auth.username ?? '?').slice(0, 2).toUpperCase())

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
  <header class="border-border/60 flex items-center gap-4 border-b px-6 py-3.5">
    <div class="relative max-w-sm flex-1">
      <Search class="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
      <input
        v-model="files.search"
        type="search"
        placeholder="Search files…"
        class="border-input bg-secondary/60 placeholder:text-muted-foreground focus-visible:ring-ring/50 h-9 w-full rounded-full border pr-4 pl-9 text-sm outline-none focus-visible:ring-3"
        @input="onSearchInput"
      />
    </div>

    <div class="text-muted-foreground hidden font-mono text-xs sm:block">
      <span class="text-foreground font-medium">{{ totals.healthy }}/{{ totals.count }}</span>
      drives ·
      <span class="text-primary font-medium">{{ formatBytes(totals.available) }}</span>
      free of {{ formatBytes(totals.total) }}
    </div>

    <Button variant="ghost" size="icon" class="rounded-full" @click="toggle">
      <Sun v-if="theme === 'dark'" class="size-4" />
      <Moon v-else class="size-4" />
    </Button>

    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <button class="flex items-center gap-2 rounded-full py-1 pr-2 pl-1 transition-colors hover:bg-accent">
          <Avatar class="size-7">
            <AvatarFallback class="bg-primary text-primary-foreground font-heading text-xs">
              {{ initials }}
            </AvatarFallback>
          </Avatar>
          <span class="hidden text-sm font-medium md:inline">{{ auth.username }}</span>
          <ChevronDown class="text-muted-foreground size-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" class="w-44">
        <DropdownMenuLabel>{{ auth.username }}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem class="text-destructive focus:text-destructive" @click="logout">
          <LogOut class="mr-2 size-4" />
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </header>
</template>
