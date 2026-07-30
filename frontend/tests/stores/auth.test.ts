import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '../../src/stores/auth'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('auth store', () => {
  it('login sets username on success', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ username: 'alice' })),
    )

    const auth = useAuthStore()
    await auth.login('alice', 'password123')

    expect(auth.username).toBe('alice')
  })

  it('login leaves username unset and throws on a 401', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ error: 'invalid username or password' }, 401)),
    )

    const auth = useAuthStore()
    await expect(auth.login('alice', 'wrong')).rejects.toThrow(
      'invalid username or password',
    )
    expect(auth.username).toBeNull()
  })

  it('checkSession marks checked=true and clears username on failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'not authenticated' }, 401)))

    const auth = useAuthStore()
    expect(auth.checked).toBe(false)
    await auth.checkSession()

    expect(auth.checked).toBe(true)
    expect(auth.username).toBeNull()
  })

  it('logout clears username', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(null, { status: 200 })),
    )

    const auth = useAuthStore()
    auth.username = 'alice'
    await auth.logout()

    expect(auth.username).toBeNull()
  })
})
