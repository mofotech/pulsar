import { useAuthStore } from '@/store/auth'
import { useProjectStore } from '@/store/project'

const API_BASE = import.meta.env.VITE_API_BASE ?? ''

interface ApiResponse<T> {
  data: T
  error: { code: string; message: string } | null
  meta: { request_id: string }
}

async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = useAuthStore.getState().token
  const { activeProjectId } = useProjectStore.getState()

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  }

  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  if (activeProjectId) {
    headers['X-Project-Id'] = activeProjectId
  }

  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers,
  })

  if (res.status === 401) {
    useAuthStore.getState().logout()
    throw new Error('Unauthorized — please log in again')
  }

  // Handle 204 No Content
  if (res.status === 204) {
    return undefined as unknown as T
  }

  const contentType = res.headers.get('content-type') ?? ''
  if (!contentType.includes('application/json')) {
    throw new Error(`Server returned ${res.status} (expected JSON, got ${contentType || 'unknown content type'})`)
  }

  const json: ApiResponse<T> = await res.json()

  if (json.error) {
    throw new Error(json.error.message)
  }

  return json.data
}

export async function apiGet<T>(path: string): Promise<T> {
  return apiFetch<T>(path, { method: 'GET' })
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method: 'POST',
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
}

export async function apiPatch<T>(path: string, body?: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method: 'PATCH',
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
}

export async function apiPut<T>(path: string, body?: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method: 'PUT',
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
}

export async function apiDelete(path: string): Promise<void> {
  await apiFetch<void>(path, { method: 'DELETE' })
}

export async function apiDeleteBody<T>(path: string, body: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method: 'DELETE',
    body: JSON.stringify(body),
  })
}
