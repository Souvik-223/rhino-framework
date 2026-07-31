<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useTheme } from '../composables/useTheme'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import logoUrl from '../assets/logo.png'
import { User, Eye, EyeOff, ArrowRight, Sun, Moon } from '@lucide/vue'

const auth = useAuthStore()
const router = useRouter()
const { theme, toggle } = useTheme()

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)
const showPassword = ref(false)

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await auth.register(username.value, password.value)
    router.push('/')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'registration failed'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="bg-vault-glow bg-blueprint relative flex min-h-svh items-center justify-center p-6">
    <Button
      variant="outline"
      size="icon-sm"
      class="absolute top-6 right-6 rounded-full"
      @click="toggle"
    >
      <Sun v-if="theme === 'dark'" class="size-3.5" />
      <Moon v-else class="size-3.5" />
    </Button>

    <Card class="animate-fade-in-up relative w-full max-w-sm border-border/60 shadow-2xl backdrop-blur-sm">
      <CardHeader class="space-y-1 text-center">
        <img :src="logoUrl" alt="Rhino" class="border-border/60 bg-card mx-auto mb-2 size-10 rounded-xl border object-cover shadow-sm" />
        <div class="font-heading text-foreground mx-auto text-xl font-bold tracking-tight uppercase">
          Rhino
        </div>
        <p class="font-heading text-muted-foreground text-[10px] font-semibold tracking-[0.2em] uppercase">
          Merge
        </p>
        <p class="text-muted-foreground pt-1 text-sm">Create your pooled drive vault</p>
      </CardHeader>
      <CardContent>
        <form class="flex flex-col gap-5" @submit.prevent="submit">
          <div class="grid gap-2">
            <Label for="username" class="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
              Username
            </Label>
            <div class="relative">
              <User class="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
              <Input id="username" v-model="username" autofocus required autocomplete="username" class="pl-9" />
            </div>
          </div>
          <div class="grid gap-2">
            <Label for="password" class="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
              Password
            </Label>
            <div class="relative">
              <Input
                id="password"
                v-model="password"
                :type="showPassword ? 'text' : 'password'"
                minlength="8"
                required
                autocomplete="new-password"
                class="pr-9"
              />
              <button
                type="button"
                class="text-muted-foreground hover:text-foreground absolute top-1/2 right-3 -translate-y-1/2"
                :aria-label="showPassword ? 'Hide password' : 'Show password'"
                @click="showPassword = !showPassword"
              >
                <EyeOff v-if="showPassword" class="size-4" />
                <Eye v-else class="size-4" />
              </button>
            </div>
            <p class="text-muted-foreground text-xs">At least 8 characters.</p>
          </div>

          <p
            v-if="error"
            class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
          >
            {{ error }}
          </p>

          <Button type="submit" size="sm" class="group w-full gap-1.5" :disabled="loading">
            {{ loading ? 'Creating account…' : 'Register' }}
            <ArrowRight class="size-3.5 transition-transform group-hover:translate-x-0.5" />
          </Button>

          <p class="text-muted-foreground text-center text-sm">
            Already have an account?
            <router-link to="/login" class="text-foreground font-medium hover:underline">
              Sign in
            </router-link>
          </p>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
