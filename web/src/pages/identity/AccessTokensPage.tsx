import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Copy, Check, KeySquare, Clock, AlertTriangle } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { toast } from '@/hooks/use-toast'
import { listPATs, createPAT, deletePAT } from '@/api/identity'
import type { PersonalAccessToken, CreatePATResponse } from '@/types/identity'

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatDate(s: string | null) {
  if (!s) return '—'
  return new Date(s).toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
}

function isExpired(t: PersonalAccessToken) {
  return t.expires_at != null && new Date(t.expires_at) < new Date()
}

// ─── Token reveal dialog ──────────────────────────────────────────────────────

function TokenRevealDialog({
  result,
  onClose,
}: {
  result: CreatePATResponse
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)

  async function handleCopy() {
    await navigator.clipboard.writeText(result.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Dialog open onOpenChange={() => onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeySquare className="h-5 w-5 text-primary" />
            Token created — copy it now
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex items-start gap-2 rounded-md border border-yellow-500/30 bg-yellow-500/10 px-3 py-2 text-sm text-yellow-400">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <p>
              This is the <strong>only time</strong> the token value will be shown. Copy it
              and store it securely — it cannot be retrieved again.
            </p>
          </div>

          <div className="space-y-1.5">
            <Label>Token</Label>
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-x-auto rounded-md border border-border bg-muted/60 px-3 py-2 text-xs font-mono text-foreground">
                {result.token}
              </code>
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="shrink-0"
                onClick={handleCopy}
              >
                {copied ? <Check className="h-4 w-4 text-green-400" /> : <Copy className="h-4 w-4" />}
              </Button>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4 text-sm">
            <div>
              <span className="text-muted-foreground">Name</span>
              <p className="font-medium">{result.name}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Expires</span>
              <p className="font-medium">{formatDate(result.expires_at)}</p>
            </div>
          </div>

          <div className="rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground space-y-1">
            <p className="font-semibold text-foreground">Usage</p>
            <p>Set the token as a Bearer header in API requests:</p>
            <code className="block text-[11px]">
              Authorization: Bearer {result.token.slice(0, 20)}…
            </code>
            <p className="mt-1">Or use it with pulsarctl:</p>
            <code className="block text-[11px]">
              pulsarctl login token --endpoint {'<api>'} --token {'<token>'}
            </code>
          </div>
        </div>

        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

interface ExpiryOption {
  label: string
  key: string
  days?: number
}

const EXPIRY_OPTIONS: ExpiryOption[] = [
  { label: 'No expiry', key: '' },
  { label: '7 days', key: '7d', days: 7 },
  { label: '30 days', key: '30d', days: 30 },
  { label: '90 days', key: '90d', days: 90 },
  { label: '1 year', key: '1y', days: 365 },
  { label: 'Custom date', key: 'custom' },
]

function CreatePATDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { name: string; expires_at: string | null }) => void
  loading: boolean
}) {
  const [name, setName] = useState('')
  const [expiryKey, setExpiryKey] = useState('')
  const [customDate, setCustomDate] = useState('')

  function reset() {
    setName('')
    setExpiryKey('')
    setCustomDate('')
  }

  function handleOpenChange(v: boolean) {
    if (!v) reset()
    onOpenChange(v)
  }

  function resolvedExpiresAt(): string | null {
    if (!expiryKey) return null
    if (expiryKey === 'custom') return customDate ? new Date(customDate).toISOString() : null
    const opt = EXPIRY_OPTIONS.find((o) => o.key === expiryKey)
    if (!opt?.days) return null
    const d = new Date()
    d.setDate(d.getDate() + opt.days)
    return d.toISOString()
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({ name: name.trim(), expires_at: resolvedExpiresAt() })
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Access Token</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="pat-name">Token Name</Label>
            <Input
              id="pat-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. terraform-prod, laptop-cli"
              required
            />
            <p className="text-xs text-muted-foreground">
              A descriptive name so you know where this token is used.
            </p>
          </div>

          <div className="space-y-2">
            <Label>Expiration</Label>
            <div className="flex flex-wrap gap-2">
              {EXPIRY_OPTIONS.map((opt) => (
                <button
                  key={opt.key}
                  type="button"
                  onClick={() => setExpiryKey(opt.key)}
                  className={`rounded-md border px-3 py-1.5 text-sm transition-colors ${
                    expiryKey === opt.key
                      ? 'border-primary bg-primary/15 text-primary font-medium'
                      : 'border-border bg-card text-muted-foreground hover:bg-muted'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
            {expiryKey === 'custom' && (
              <Input
                type="datetime-local"
                value={customDate}
                onChange={(e) => setCustomDate(e.target.value)}
                min={new Date().toISOString().slice(0, 16)}
              />
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => handleOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading || !name.trim()}>
              {loading ? 'Creating…' : 'Create Token'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function AccessTokensPage() {
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<PersonalAccessToken | null>(null)
  const [revealed, setRevealed] = useState<CreatePATResponse | null>(null)

  const { data: tokens = [], isLoading, error } = useQuery({
    queryKey: ['pats'],
    queryFn: listPATs,
  })

  const createMutation = useMutation({
    mutationFn: createPAT,
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['pats'] })
      setCreateOpen(false)
      setRevealed(res)
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Failed to create token', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deletePAT,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['pats'] })
      setDeleteTarget(null)
      toast({ title: 'Access token revoked' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Failed to revoke token', description: err.message })
    },
  })

  const columns: Column<PersonalAccessToken>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (row) => (
        <span className="flex items-center gap-2">
          <KeySquare className="h-4 w-4 text-muted-foreground shrink-0" />
          <span className={isExpired(row) ? 'line-through text-muted-foreground' : ''}>{row.name}</span>
          {isExpired(row) && (
            <span className="rounded-full bg-destructive/15 px-2 py-0.5 text-[10px] font-semibold uppercase text-destructive">
              Expired
            </span>
          )}
        </span>
      ),
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (row) => formatDate(row.created_at),
    },
    {
      key: 'last_used_at',
      header: 'Last used',
      render: (row) => (
        <span className="flex items-center gap-1.5 text-muted-foreground">
          {row.last_used_at ? (
            <>
              <Clock className="h-3.5 w-3.5" />
              {formatDate(row.last_used_at)}
            </>
          ) : (
            'Never'
          )}
        </span>
      ),
    },
    {
      key: 'expires_at',
      header: 'Expires',
      render: (row) =>
        row.expires_at ? (
          <span className={isExpired(row) ? 'text-destructive' : 'text-muted-foreground'}>
            {formatDate(row.expires_at)}
          </span>
        ) : (
          <span className="text-muted-foreground">Never</span>
        ),
    },
    {
      key: 'id',
      header: '',
      render: (row) => (
        <Button
          size="sm"
          variant="ghost"
          className="text-destructive hover:text-destructive"
          onClick={() => setDeleteTarget(row)}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Access Tokens"
        description="Long-lived tokens for CLI, Terraform, and other automated tooling."
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="mr-2 h-4 w-4" />
            New Token
          </Button>
        }
      />

      <ResourceTable
        columns={columns}
        data={tokens}
        isLoading={isLoading}
        error={error as Error | null}
        emptyMessage="No access tokens. Create one to authenticate CLI tools and IaC pipelines."
      />

      <CreatePATDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(req) => createMutation.mutate(req)}
        loading={createMutation.isPending}
      />

      {revealed && (
        <TokenRevealDialog result={revealed} onClose={() => setRevealed(null)} />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => { if (!v) setDeleteTarget(null) }}
        title="Revoke access token"
        description={`Revoke "${deleteTarget?.name}"? Any tools using this token will immediately lose access.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}
