import { describe, expect, it } from 'vitest'
import { apiDelete, apiGet, apiPost } from './client'
import { server, http, HttpResponse, ok, apiError } from '@/test/server'
import { useAuthStore } from '@/store/auth'
import { useProjectStore } from '@/store/project'
import { inOneHour, makeJwt } from '@/test/jwt'

describe('api client', () => {
  it('sends the bearer token and active project, and unwraps the data envelope', async () => {
    useAuthStore.getState().login(makeJwt({ sub: 'u1' }), inOneHour(), 'a@example.test')
    // login() clears the active project asynchronously; let that settle first.
    await new Promise((r) => setTimeout(r, 0))
    useProjectStore.getState().setActiveProject('proj-9', 'nine')

    let seen: Headers | undefined
    server.use(http.get('*/v1/things', ({ request }) => {
      seen = request.headers
      return ok([{ id: 1 }])
    }))

    await expect(apiGet('/v1/things')).resolves.toEqual([{ id: 1 }])
    expect(seen?.get('authorization')).toBe(`Bearer ${useAuthStore.getState().token}`)
    expect(seen?.get('x-project-id')).toBe('proj-9')
    expect(seen?.get('content-type')).toBe('application/json')
  })

  it('sends no auth headers when logged out', async () => {
    let seen: Headers | undefined
    server.use(http.get('*/v1/public', ({ request }) => { seen = request.headers; return ok(null) }))
    await apiGet('/v1/public')
    expect(seen?.has('authorization')).toBe(false)
    expect(seen?.has('x-project-id')).toBe(false)
  })

  it('serialises request bodies as JSON', async () => {
    let body: unknown
    server.use(http.post('*/v1/things', async ({ request }) => { body = await request.json(); return ok({ id: 2 }, 201) }))
    await apiPost('/v1/things', { name: 'x', n: 1 })
    expect(body).toEqual({ name: 'x', n: 1 })
  })

  it('surfaces the server error message', async () => {
    server.use(http.post('*/v1/things', () => apiError(422, 'UNPROCESSABLE', 'hypervisor_type "lxd" is not supported')))
    await expect(apiPost('/v1/things', {})).rejects.toThrow('hypervisor_type "lxd" is not supported')
  })

  it('logs the user out on 401', async () => {
    useAuthStore.getState().login(makeJwt({ sub: 'u1' }), inOneHour(), 'a@example.test')
    server.use(http.get('*/v1/things', () => apiError(401, 'UNAUTHORIZED', 'token has been revoked')))
    await expect(apiGet('/v1/things')).rejects.toThrow(/log in again/)
    expect(useAuthStore.getState().token).toBeNull()
  })

  it('returns undefined for 204 No Content', async () => {
    server.use(http.delete('*/v1/things/1', () => new HttpResponse(null, { status: 204 })))
    await expect(apiDelete('/v1/things/1')).resolves.toBeUndefined()
  })

  it('reports non-JSON responses (e.g. a proxy error page) clearly', async () => {
    server.use(http.get('*/v1/things', () => new HttpResponse('<html>502 Bad Gateway</html>', {
      status: 502, headers: { 'Content-Type': 'text/html' },
    })))
    await expect(apiGet('/v1/things')).rejects.toThrow(/502.*expected JSON/)
  })
})
