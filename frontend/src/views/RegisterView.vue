<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

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
  <div class="auth-page">
    <form class="auth-card card" @submit.prevent="submit">
      <h1>Create your Rhino account</h1>
      <label>
        Username
        <input v-model="username" autofocus required />
      </label>
      <label>
        Password
        <input v-model="password" type="password" minlength="8" required />
      </label>
      <p v-if="error" class="auth-error">{{ error }}</p>
      <button class="btn btn-primary" type="submit" :disabled="loading">Register</button>
      <router-link to="/login">Already have an account? Sign in</router-link>
    </form>
  </div>
</template>

<style scoped>
.auth-page {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.auth-card {
  width: 320px;
  padding: 2rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.auth-card h1 {
  font-size: 1.3rem;
  margin: 0 0 0.5rem;
}

.auth-card label {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  font-size: 0.85rem;
  color: var(--text-dim);
}

.auth-card input {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.5rem 0.6rem;
  background: var(--bg);
  color: var(--text);
  font-size: 0.95rem;
}

.auth-error {
  color: var(--danger);
  font-size: 0.85rem;
  margin: 0;
}
</style>
