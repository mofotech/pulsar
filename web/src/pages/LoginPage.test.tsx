import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import LoginPage from './LoginPage'
import { renderAt } from '@/test/render'
import { server, http, ok, apiError } from '@/test/server'
import { inOneHour, makeJwt } from '@/test/jwt'
import { useAuthStore } from '@/store/auth'

const token = makeJwt({ sub: 'u1', role: 'member', org_role: 'org_admin', org_id: 'o1', project_id: 'p1' })

function mockIdps(idps: unknown[]) {
  let lookedUp: string | null = null
  server.use(http.get('*/v1/auth/idps', ({ request }) => {
    lookedUp = new URL(request.url).searchParams.get('email')
    return ok(idps)
  }))
  return () => lookedUp
}

async function enterEmail(email = 'ada@example.test') {
  const user = userEvent.setup()
  renderAt(<LoginPage />, { route: '/login', path: '/login' })
  await user.type(screen.getByLabelText(/email address/i), email)
  await user.click(screen.getByRole('button', { name: /continue/i }))
  await screen.findByLabelText(/password/i)
  return user
}

describe('LoginPage', () => {
  it('looks up SSO providers for the email and links to their authorize endpoint', async () => {
    const lookedUp = mockIdps([{ id: 'i1', name: 'Acme SSO', slug: 'acme-sso' }])
    await enterEmail('ada@acme.test')

    expect(lookedUp()).toBe('ada@acme.test')
    const sso = screen.getByRole('link', { name: /continue with acme sso/i })
    expect(sso).toHaveAttribute('href', '/v1/auth/oidc/acme-sso/authorize')
    expect(screen.getByText(/signing in as ada@acme.test/i)).toBeInTheDocument()
  })

  it('signs in with a password, stores the session and opens the console', async () => {
    mockIdps([])
    let body: unknown
    server.use(http.post('*/v1/auth/tokens', async ({ request }) => {
      body = await request.json()
      return ok({ token, expires_at: inOneHour() }, 201)
    }))
    const user = await enterEmail()
    expect(screen.queryByRole('link', { name: /continue with/i })).not.toBeInTheDocument()

    await user.type(screen.getByLabelText(/password/i), 'hunter2')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/compute/instances'))
    expect(body).toEqual({ email: 'ada@example.test', password: 'hunter2' })
    const s = useAuthStore.getState()
    expect(s.token).toBe(token)
    expect(s.email).toBe('ada@example.test')
    expect(s.isOrgAdmin()).toBe(true)
  })

  // A failed login is a 401 too, but it is not an expired session: show the
  // server's reason, not "please log in again".
  it('shows the server error for wrong credentials', async () => {
    mockIdps([])
    server.use(http.post('*/v1/auth/tokens', () => apiError(401, 'UNAUTHORIZED', 'invalid credentials')))
    const user = await enterEmail()
    await user.type(screen.getByLabelText(/password/i), 'wrong')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    expect(await screen.findByText('invalid credentials')).toBeInTheDocument()
    expect(screen.getByTestId('location')).toHaveTextContent('/login')
    expect(useAuthStore.getState().token).toBeNull()
  })

  it('still offers password login when the SSO lookup fails', async () => {
    server.use(http.get('*/v1/auth/idps', () => apiError(500, 'INTERNAL_ERROR', 'boom')))
    await enterEmail()
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /continue with/i })).not.toBeInTheDocument()
  })

  it('can go back and use a different email', async () => {
    mockIdps([{ id: 'i1', name: 'Acme SSO', slug: 'acme-sso' }])
    const user = await enterEmail()
    await user.click(screen.getByRole('button', { name: /different email/i }))
    expect(screen.getByLabelText(/email address/i)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /continue with/i })).not.toBeInTheDocument()
  })

  // OIDCCallbackPage redirects here with ?error=... when SSO doesn't complete.
  it('explains a failed SSO sign-in passed back via ?error=', () => {
    renderAt(<LoginPage />, { route: '/login?error=oidc_callback_missing_token', path: '/login' })
    expect(screen.getByRole('alert')).toHaveTextContent(/single sign-on did not complete/i)
  })
})
