import { describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '../../src/api/client'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

describe('api client', () => {
  it('resolves with parsed JSON on a 2xx response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse([{ label: 'home-gmail' }])),
    )

    const accounts = await api.listAccounts()
    expect(accounts).toEqual([{ label: 'home-gmail' }])
  })

  it('throws ApiError carrying the status and server-provided message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ error: 'username already taken' }, 409)),
    )

    await expect(api.register('bob', 'password123')).rejects.toMatchObject({
      status: 409,
      message: 'username already taken',
    })
  })

  it('throws ApiError even when the error body is not JSON', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('internal server error', { status: 500, statusText: 'Internal Server Error' }),
      ),
    )

    await expect(api.me()).rejects.toBeInstanceOf(ApiError)
  })

  it('resolves undefined for a 204 No Content response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    await expect(api.removeAccount('home-gmail')).resolves.toBeUndefined()
  })
})
