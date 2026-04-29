import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Zap } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuthStore } from '@/store/auth'
import { createToken } from '@/api/auth'
import { lookupIdps } from '@/api/identity'
import type { IdentityProvider } from '@/types/identity'

// Derive the SSO authorize URL from the IDP slug.
function ssoUrl(idp: IdentityProvider) {
  return `/v1/auth/oidc/${idp.slug}/authorize`
}

export default function LoginPage() {
  const navigate = useNavigate()
  const login = useAuthStore((s) => s.login)

  // Step 1 — email entry; step 2 — password (+ optional SSO buttons)
  const [step, setStep] = useState<'email' | 'password'>('email')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [idps, setIdps] = useState<IdentityProvider[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleEmailNext(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      const found = await lookupIdps(email)
      setIdps(found)
    } catch {
      // Network error — still allow password login
      setIdps([])
    } finally {
      setLoading(false)
      setStep('password')
    }
  }

  async function handlePasswordSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      const res = await createToken(email, password)
      login(res.token, res.expires_at, email)
      navigate('/compute/instances')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-background p-4">
      {/* Background grid */}
      <div
        className="pointer-events-none absolute inset-0 opacity-[0.03]"
        style={{
          backgroundImage:
            'linear-gradient(hsl(var(--foreground)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--foreground)) 1px, transparent 1px)',
          backgroundSize: '64px 64px',
        }}
      />

      {/* Glowing orbs */}
      <div className="pointer-events-none absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2">
        <div
          className="orb h-[600px] w-[600px] rounded-full opacity-[0.12]"
          style={{
            background: 'radial-gradient(circle, hsl(var(--primary)) 0%, transparent 70%)',
          }}
        />
      </div>
      <div className="pointer-events-none absolute right-1/4 top-1/4">
        <div
          className="orb h-[300px] w-[300px] rounded-full opacity-[0.06]"
          style={{
            background: 'radial-gradient(circle, hsl(210 80% 60%) 0%, transparent 70%)',
            animationDelay: '-6s',
          }}
        />
      </div>

      {/* Card */}
      <div className="relative z-10 w-full max-w-sm">
        {/* Brand */}
        <div className="mb-8 flex flex-col items-center gap-3">
          <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/15 ring-1 ring-primary/30">
            <Zap className="h-6 w-6 text-primary" />
          </div>
          <div className="text-center">
            <h1 className="text-xl font-semibold tracking-tight text-foreground">Pulsar</h1>
            <p className="text-xs text-muted-foreground">IaaS Orchestration Platform</p>
          </div>
        </div>

        <div className="rounded-xl border border-border/60 bg-card shadow-xl">
          <div className="p-6">
            <div className="mb-5">
              <h2 className="text-sm font-semibold text-foreground">Sign in to console</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {step === 'email' ? 'Enter your email to continue' : `Signing in as ${email}`}
              </p>
            </div>

            {step === 'email' ? (
              /* ── Step 1: email ── */
              <form onSubmit={handleEmailNext} className="space-y-4">
                <div className="space-y-1.5">
                  <Label htmlFor="email" className="text-xs font-medium text-muted-foreground">
                    Email address
                  </Label>
                  <Input
                    id="email"
                    type="email"
                    placeholder="you@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    autoFocus
                  />
                </div>
                <Button type="submit" className="mt-2 w-full" disabled={loading}>
                  {loading ? (
                    <span className="flex items-center gap-2">
                      <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground" />
                      Checking…
                    </span>
                  ) : (
                    'Continue'
                  )}
                </Button>
              </form>
            ) : (
              /* ── Step 2: SSO buttons + password ── */
              <div className="space-y-4">
                {/* SSO options */}
                {idps.length > 0 && (
                  <div className="space-y-2">
                    {idps.map((idp) => (
                      <a key={idp.id} href={ssoUrl(idp)} className="block">
                        <Button type="button" variant="outline" className="w-full justify-center gap-2">
                          <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                            <circle cx="12" cy="12" r="10" />
                            <path d="M12 8v4l3 3" />
                          </svg>
                          Continue with {idp.name}
                        </Button>
                      </a>
                    ))}

                    <div className="relative flex items-center gap-3 py-1">
                      <div className="h-px flex-1 bg-border/50" />
                      <span className="text-[11px] text-muted-foreground/60">or use password</span>
                      <div className="h-px flex-1 bg-border/50" />
                    </div>
                  </div>
                )}

                {/* Password form */}
                <form onSubmit={handlePasswordSubmit} className="space-y-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="password" className="text-xs font-medium text-muted-foreground">
                      Password
                    </Label>
                    <Input
                      id="password"
                      type="password"
                      placeholder="••••••••"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      required
                      autoFocus
                    />
                  </div>

                  {error && (
                    <div className="flex items-start gap-2 rounded-lg bg-destructive/10 px-3 py-2.5 text-xs text-destructive ring-1 ring-destructive/20">
                      <span className="mt-0.5 h-1.5 w-1.5 shrink-0 rounded-full bg-destructive" />
                      {error}
                    </div>
                  )}

                  <Button type="submit" className="mt-2 w-full" disabled={loading}>
                    {loading ? (
                      <span className="flex items-center gap-2">
                        <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground" />
                        Signing in…
                      </span>
                    ) : (
                      'Sign in'
                    )}
                  </Button>
                </form>

                <button
                  type="button"
                  onClick={() => { setStep('email'); setError(null); setIdps([]) }}
                  className="w-full text-center text-[11px] text-muted-foreground/60 hover:text-muted-foreground transition-colors"
                >
                  ← Use a different email
                </button>
              </div>
            )}
          </div>
        </div>

        <p className="mt-6 text-center text-[11px] text-muted-foreground/50">
          Pulsar Infrastructure Platform
        </p>
      </div>
    </div>
  )
}
