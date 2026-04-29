import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  token: string | null
  expiresAt: string | null
  email: string | null
  /** project_id embedded in the JWT — the user's home project */
  jwtProjectId: string | null
  /** role embedded in the JWT — 'admin' | 'member' */
  role: string | null
  /** org_role embedded in the JWT — 'platform_admin' | 'org_admin' | 'member' */
  orgRole: string | null
  /** org_id embedded in the JWT */
  orgId: string | null
  login: (token: string, expiresAt: string, email: string) => void
  logout: () => void
  isAuthenticated: () => boolean
  isAdmin: () => boolean
  isPlatformAdmin: () => boolean
  isOrgAdmin: () => boolean
}

interface JwtPayload {
  project_id?: string
  role?: string
  org_role?: string
  org_id?: string
}

function parseJwtPayload(token: string): JwtPayload {
  try {
    return JSON.parse(atob(token.split('.')[1])) as JwtPayload
  } catch {
    return {}
  }
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      token: null,
      expiresAt: null,
      email: null,
      jwtProjectId: null,
      role: null,
      orgRole: null,
      orgId: null,
      login: (token, expiresAt, email) => {
        const payload = parseJwtPayload(token)
        // Clear any stale project selection from a previous session so the
        // TopBar re-seeds from this user's JWT / project list on first load.
        import('@/store/project').then(({ useProjectStore }) => {
          useProjectStore.getState().clearActiveProject()
        })
        set({
          token,
          expiresAt,
          email,
          jwtProjectId: payload.project_id ?? null,
          role: payload.role ?? null,
          orgRole: payload.org_role ?? null,
          orgId: payload.org_id ?? null,
        })
      },
      logout: () => {
        // Clear active project so the next login starts with a clean slate.
        import('@/store/project').then(({ useProjectStore }) => {
          useProjectStore.getState().clearActiveProject()
        })
        set({
          token: null, expiresAt: null, email: null,
          jwtProjectId: null, role: null, orgRole: null, orgId: null,
        })
      },
      isAuthenticated: () => {
        const { token, expiresAt } = get()
        if (!token || !expiresAt) return false
        return new Date(expiresAt) > new Date()
      },
      isAdmin: () => get().role === 'admin',
      isPlatformAdmin: () => get().orgRole === 'platform_admin',
      isOrgAdmin: () => get().orgRole === 'org_admin' || get().orgRole === 'platform_admin',
    }),
    {
      name: 'pulsar-auth',
    }
  )
)
