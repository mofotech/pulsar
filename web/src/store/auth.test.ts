import { describe, expect, it, vi } from 'vitest'
import { useAuthStore } from './auth'
import { useProjectStore } from './project'
import { inOneHour, makeJwt } from '@/test/jwt'

const claims = {
  sub: 'u1', project_id: 'p1', role: 'admin', org_role: 'platform_admin', org_id: 'o1',
}

describe('auth store', () => {
  it('reads role, org role and project from the JWT on login', () => {
    useAuthStore.getState().login(makeJwt(claims), inOneHour(), 'a@example.test')
    const s = useAuthStore.getState()
    expect(s.jwtProjectId).toBe('p1')
    expect(s.isAuthenticated()).toBe(true)
    expect(s.isAdmin()).toBe(true)
    expect(s.isPlatformAdmin()).toBe(true)
    expect(s.isOrgAdmin()).toBe(true)
  })

  // JWTs are base64url (with '-' / '_') and may carry UTF-8; plain atob()
  // rejects the former and garbles the latter, silently dropping all claims.
  it('decodes base64url payloads containing - and _ and UTF-8', () => {
    const token = makeJwt({ ...claims, email: 'zoë~?>@exämple.test', name: 'Ünïcødé ~~~ ??? >>>' })
    const payload = token.split('.')[1]
    expect(payload).toMatch(/[-_]/) // precondition: genuinely base64url-only characters
    useAuthStore.getState().login(token, inOneHour(), 'zoë@example.test')
    const s = useAuthStore.getState()
    expect(s.role).toBe('admin')
    expect(s.orgRole).toBe('platform_admin')
    expect(s.orgId).toBe('o1')
  })

  it('treats malformed tokens as having no claims rather than throwing', () => {
    useAuthStore.getState().login('not-a-jwt', inOneHour(), 'x@example.test')
    expect(useAuthStore.getState().role).toBeNull()
    expect(useAuthStore.getState().isAdmin()).toBe(false)
  })

  it('is not authenticated once the token has expired', () => {
    useAuthStore.getState().login(makeJwt(claims), new Date(Date.now() - 1000).toISOString(), 'a@example.test')
    expect(useAuthStore.getState().isAuthenticated()).toBe(false)
  })

  it('logout clears identity and the active project', async () => {
    useAuthStore.getState().login(makeJwt(claims), inOneHour(), 'a@example.test')
    useProjectStore.getState().setActiveProject('p2', 'other')
    useAuthStore.getState().logout()
    expect(useAuthStore.getState().token).toBeNull()
    expect(useAuthStore.getState().isAdmin()).toBe(false)
    await vi.waitFor(() => expect(useProjectStore.getState().activeProjectId).toBeNull())
  })
})
