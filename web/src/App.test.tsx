import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import App from './App'
import { renderAt } from '@/test/render'
import { server, http, ok } from '@/test/server'
import { inOneHour, makeJwt } from '@/test/jwt'
import { useAuthStore } from '@/store/auth'

describe('route guard', () => {
  it('redirects to login when there is no session', async () => {
    renderAt(<App />, { route: '/compute/instances' })
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/))
    expect(screen.getByLabelText(/email address/i)).toBeInTheDocument()
  })

  it('redirects to login when the session has expired', async () => {
    useAuthStore.getState().login(makeJwt({ sub: 'u1' }), new Date(Date.now() - 1000).toISOString(), 'a@example.test')
    renderAt(<App />, { route: '/storage/volumes' })
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/))
  })

  it('lets an authenticated user into the console', async () => {
    // The shell loads projects, instances, etc.; answer every API call with an empty list.
    server.use(http.get('*/v1/*', () => ok([])))
    useAuthStore.getState().login(makeJwt({ sub: 'u1', project_id: 'p1' }), inOneHour(), 'a@example.test')
    renderAt(<App />, { route: '/identity/access-tokens' })
    expect(await screen.findByRole('heading', { name: /access tokens/i })).toBeInTheDocument()
    expect(screen.getByTestId('location')).toHaveTextContent('/identity/access-tokens')
  })
})
