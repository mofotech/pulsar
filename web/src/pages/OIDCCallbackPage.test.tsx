import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import OIDCCallbackPage from './OIDCCallbackPage'
import { renderAt } from '@/test/render'
import { makeJwt } from '@/test/jwt'
import { useAuthStore } from '@/store/auth'

describe('OIDCCallbackPage', () => {
  it('stores the SSO token, takes the email from it, and drops the token from the URL', async () => {
    const token = makeJwt({ sub: 'u1', email: 'sso.ünïcode@acme.test', org_role: 'member' })
    const expires = new Date(Date.now() + 3600_000).toISOString()
    renderAt(<OIDCCallbackPage />, {
      route: `/auth/callback?token=${token}&expires_at=${encodeURIComponent(expires)}`,
      path: '/auth/callback',
    })

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/compute/instances'))
    expect(screen.getByTestId('location')).not.toHaveTextContent('token=')
    const s = useAuthStore.getState()
    expect(s.token).toBe(token)
    expect(s.email).toBe('sso.ünïcode@acme.test')
    expect(s.isAuthenticated()).toBe(true)
  })

  it('sends the user back to login with an error when the token is missing', async () => {
    renderAt(<OIDCCallbackPage />, { route: '/auth/callback?expires_at=x', path: '/auth/callback' })
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/login?error=oidc_callback_missing_token'))
    expect(useAuthStore.getState().token).toBeNull()
  })
})
