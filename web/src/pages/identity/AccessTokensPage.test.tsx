import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AccessTokensPage from './AccessTokensPage'
import { renderAt } from '@/test/render'
import { server, http, HttpResponse, ok, apiError } from '@/test/server'
import type { PersonalAccessToken } from '@/types/identity'

const existing: PersonalAccessToken = {
  id: 't1', name: 'ci-pipeline', last_used_at: null, expires_at: null,
  created_at: '2026-11-01T10:00:00Z',
}

describe('AccessTokensPage', () => {
  it('lists tokens without ever showing a token value', async () => {
    server.use(http.get('*/v1/auth/tokens/personal', () => ok([existing])))
    renderAt(<AccessTokensPage />)
    expect(await screen.findByText('ci-pipeline')).toBeInTheDocument()
    expect(screen.queryByText(/pat_/)).not.toBeInTheDocument()
  })

  it('creates a token with the chosen expiry and reveals it exactly once', async () => {
    const tokens = [existing]
    let created: { name: string; expires_at?: string | null } | undefined
    server.use(
      http.get('*/v1/auth/tokens/personal', () => ok(tokens)),
      http.post('*/v1/auth/tokens/personal', async ({ request }) => {
        created = (await request.json()) as typeof created
        const row = { ...existing, id: 't2', name: created!.name, expires_at: created!.expires_at ?? null }
        tokens.push(row)
        return ok({ ...row, token: 'pat_SECRET_shown_once' }, 201)
      }),
    )
    const user = userEvent.setup()
    renderAt(<AccessTokensPage />)
    await screen.findByText('ci-pipeline')

    await user.click(screen.getByRole('button', { name: /new token/i }))
    await user.type(screen.getByLabelText(/token name/i), 'terraform-prod')
    await user.click(screen.getByRole('button', { name: '30 days' }))
    await user.click(screen.getByRole('button', { name: /create token/i }))

    const reveal = await screen.findByRole('dialog', { name: /copy it now/i })
    expect(within(reveal).getByText('pat_SECRET_shown_once')).toBeInTheDocument()
    expect(created?.name).toBe('terraform-prod')
    const days = (new Date(created!.expires_at!).getTime() - Date.now()) / 86_400_000
    expect(days).toBeGreaterThan(29)
    expect(days).toBeLessThan(31)

    await user.click(within(reveal).getByRole('button', { name: /done/i }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findByText('terraform-prod')).toBeInTheDocument()
    expect(screen.queryByText('pat_SECRET_shown_once')).not.toBeInTheDocument()
  })

  it('requires a name before a token can be created', async () => {
    server.use(http.get('*/v1/auth/tokens/personal', () => ok([])))
    const user = userEvent.setup()
    renderAt(<AccessTokensPage />)
    await user.click(await screen.findByRole('button', { name: /new token/i }))
    expect(screen.getByRole('button', { name: /create token/i })).toBeDisabled()
  })

  it('revokes a token after confirmation', async () => {
    let tokens = [existing]
    let deleted: string | undefined
    server.use(
      http.get('*/v1/auth/tokens/personal', () => ok(tokens)),
      http.delete('*/v1/auth/tokens/personal/:id', ({ params }) => {
        deleted = params.id as string
        tokens = []
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    renderAt(<AccessTokensPage />)

    await user.click(await screen.findByRole('button', { name: /revoke ci-pipeline/i }))
    const confirm = screen.getByRole('dialog', { name: /revoke access token/i })
    expect(confirm).toHaveTextContent(/ci-pipeline/)
    await user.click(within(confirm).getByRole('button', { name: /confirm/i }))

    await waitFor(() => expect(deleted).toBe('t1'))
    await waitFor(() => expect(screen.queryByText('ci-pipeline')).not.toBeInTheDocument())
  })

  it('shows the error when loading tokens fails', async () => {
    server.use(http.get('*/v1/auth/tokens/personal', () => apiError(500, 'INTERNAL_ERROR', 'token lookup failed')))
    renderAt(<AccessTokensPage />)
    expect(await screen.findByText(/token lookup failed/)).toBeInTheDocument()
  })
})
