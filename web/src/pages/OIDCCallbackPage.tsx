import { useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuthStore } from '@/store/auth'
import { decodeJwtPayload } from '@/lib/jwt'

/**
 * OIDCCallbackPage
 *
 * The backend redirects here after a successful OIDC login with the Pulsar JWT
 * as a query parameter:  /#/auth/callback?token=<jwt>&expires_at=<iso>
 *
 * We read those values, call login(), then send the user to the dashboard.
 */
export default function OIDCCallbackPage() {
  const [params] = useSearchParams()
  const login = useAuthStore((s) => s.login)
  const navigate = useNavigate()

  useEffect(() => {
    const token = params.get('token')
    const expiresAt = params.get('expires_at')
    if (token && expiresAt) {
      const email = decodeJwtPayload<{ email: string }>(token).email ?? ''
      login(token, expiresAt, email)
      navigate('/compute/instances', { replace: true })
    } else {
      // Missing token — redirect to login with an error hint.
      navigate('/login?error=oidc_callback_missing_token', { replace: true })
    }
  }, [params, login, navigate])

  return (
    <div className="flex min-h-screen items-center justify-center">
      <p className="text-muted-foreground text-sm">Completing sign-in…</p>
    </div>
  )
}
