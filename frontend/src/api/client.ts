export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    credentials: 'same-origin',
    ...init,
    headers: {
      ...(init?.body && !(init.body instanceof FormData)
        ? { 'Content-Type': 'application/json' }
        : {}),
      ...init?.headers,
    },
  })

  if (!res.ok) {
    let message = res.statusText
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {}
    throw new ApiError(res.status, message)
  }

  if (res.status === 204) return undefined as T
  const contentType = res.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    return (await res.json()) as T
  }
  return undefined as T
}

export interface AccountStatus {
  label: string
  limit?: number
  usage?: number
  available?: number
  unlimited?: boolean
  photoUrl?: string
  error?: string
}

export interface VirtualFile {
  name: string
  size: number
  status: string
  modifiedAt: string
  accounts: string[]
  degraded: boolean
}

export const api = {
  register: (username: string, password: string) =>
    request<{ username: string }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  login: (username: string, password: string) =>
    request<{ username: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  logout: () => request<void>('/auth/logout', { method: 'POST' }),

  me: () => request<{ username: string }>('/me'),

  listAccounts: () => request<AccountStatus[]>('/accounts'),

  connectAccountUrl: (label: string) => `/api/accounts/connect?label=${encodeURIComponent(label)}`,

  removeAccount: (label: string, force = false) =>
    request<void>(`/accounts/${encodeURIComponent(label)}?force=${force}`, { method: 'DELETE' }),

  listFiles: (prefix = '') =>
    request<VirtualFile[]>(`/files${prefix ? `?prefix=${encodeURIComponent(prefix)}` : ''}`),

  uploadFile: (file: File, onProgress?: (fraction: number) => void) =>
    new Promise<{ name: string }>((resolve, reject) => {
      const form = new FormData()
      form.append('file', file)

      const xhr = new XMLHttpRequest()
      xhr.open('POST', '/api/files')
      xhr.withCredentials = true
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total)
      }
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(JSON.parse(xhr.responseText))
        } else {
          let message = xhr.statusText
          try {
            message = JSON.parse(xhr.responseText)?.error ?? message
          } catch {}
          reject(new ApiError(xhr.status, message))
        }
      }
      xhr.onerror = () => reject(new ApiError(0, 'network error'))
      xhr.send(form)
    }),

  downloadFile: async (name: string) => {
    const res = await fetch(`/api/files/download?name=${encodeURIComponent(name)}`, {
      credentials: 'same-origin',
    })
    if (!res.ok) {
      let message = res.statusText
      try {
        const body = await res.json()
        if (body?.error) message = body.error
      } catch {}
      throw new ApiError(res.status, message)
    }
    return res.blob()
  },

  deleteFile: (name: string, purge: boolean) =>
    request<void>(`/files?name=${encodeURIComponent(name)}&purge=${purge}`, {
      method: 'DELETE',
    }),
}
