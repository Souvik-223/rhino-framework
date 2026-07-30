<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const auth = useAuthStore()
const router = useRouter()

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

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
    <Card class="animate-fade-in-up relative w-full max-w-sm border-border/60 shadow-2xl backdrop-blur-sm">
      <CardHeader class="space-y-1 text-center">
        <div
          class="font-heading text-primary mx-auto mb-1 text-3xl font-bold tracking-tight uppercase"
        >
          Rhino
        </div>
        <p class="text-muted-foreground text-sm">Create your pooled drive vault</p>
      </CardHeader>
      <CardContent>
        <form class="flex flex-col gap-5" @submit.prevent="submit">
          <div class="grid gap-2">
            <Label for="username">Username</Label>
            <Input id="username" v-model="username" autofocus required autocomplete="username" />
          </div>
          <div class="grid gap-2">
            <Label for="password">Password</Label>
            <Input
              id="password"
              v-model="password"
              type="password"
              minlength="8"
              required
              autocomplete="new-password"
            />
            <p class="text-muted-foreground text-xs">At least 8 characters.</p>
          </div>

          <p
            v-if="error"
            class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
          >
            {{ error }}
          </p>

          <Button type="submit" class="w-full" :disabled="loading">
            {{ loading ? 'Creating account…' : 'Register' }}
          </Button>

          <p class="text-muted-foreground text-center text-sm">
            Already have an account?
            <router-link to="/login" class="text-primary font-medium hover:underline">
              Sign in
            </router-link>
          </p>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
