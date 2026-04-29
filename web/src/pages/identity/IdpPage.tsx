import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Pencil, Trash2, ToggleLeft, ToggleRight } from 'lucide-react'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from '@/hooks/use-toast'
import { listIdps, createIdp, updateIdp, deleteIdp } from '@/api/identity'
import { useAuthStore } from '@/store/auth'
import type { IdentityProvider } from '@/types/identity'

export default function IdpPage() {
  const qc = useQueryClient()
  const orgId = useAuthStore((s) => s.orgId)
  const isOrgAdmin = useAuthStore((s) => s.isOrgAdmin)()

  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<IdentityProvider | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<IdentityProvider | null>(null)

  const { data: idps = [], isLoading, error } = useQuery({
    queryKey: ['idps', orgId],
    queryFn: () => listIdps(orgId!),
    enabled: !!orgId,
  })

  const createMutation = useMutation({
    mutationFn: (req: Parameters<typeof createIdp>[1]) => createIdp(orgId!, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['idps', orgId] })
      setCreateOpen(false)
      toast({ title: 'Identity provider created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ idpId, req }: { idpId: string; req: Parameters<typeof updateIdp>[2] }) =>
      updateIdp(orgId!, idpId, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['idps', orgId] })
      setEditTarget(null)
      toast({ title: 'Identity provider updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (idpId: string) => deleteIdp(orgId!, idpId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['idps', orgId] })
      setDeleteTarget(null)
      toast({ title: 'Identity provider deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const toggleMutation = useMutation({
    mutationFn: ({ idpId, enabled }: { idpId: string; enabled: boolean }) =>
      updateIdp(orgId!, idpId, { enabled }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['idps', orgId] })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Toggle failed', description: err.message })
    },
  })

  const columns: Column<IdentityProvider>[] = [
    { key: 'name', header: 'Name' },
    { key: 'provider_type', header: 'Type', render: (r) => <span className="uppercase text-xs font-mono">{r.provider_type}</span> },
    { key: 'issuer_url', header: 'Issuer URL', render: (r) => <span className="font-mono text-xs truncate max-w-xs block">{r.issuer_url}</span> },
    {
      key: 'enabled',
      header: 'Status',
      render: (r) => (
        <span className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-semibold ${
          r.enabled ? 'bg-emerald-500/15 text-emerald-400' : 'bg-muted text-muted-foreground'
        }`}>
          {r.enabled ? <ToggleRight className="h-3 w-3" /> : <ToggleLeft className="h-3 w-3" />}
          {r.enabled ? 'Enabled' : 'Disabled'}
        </span>
      ),
    },
    ...(isOrgAdmin ? [{
      key: 'actions' as keyof IdentityProvider,
      header: '',
      render: (row: IdentityProvider) => (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            title={row.enabled ? 'Disable' : 'Enable'}
            onClick={(e) => {
              e.stopPropagation()
              toggleMutation.mutate({ idpId: row.id, enabled: !row.enabled })
            }}
          >
            {row.enabled
              ? <ToggleRight className="h-4 w-4 text-emerald-400" />
              : <ToggleLeft className="h-4 w-4 text-muted-foreground" />}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            title="Edit"
            onClick={(e) => { e.stopPropagation(); setEditTarget(row) }}
          >
            <Pencil className="h-4 w-4 text-muted-foreground" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            title="Delete"
            onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
          >
            <Trash2 className="h-4 w-4 text-muted-foreground" />
          </Button>
        </div>
      ),
    }] : []),
  ]

  if (!orgId) {
    return (
      <div className="p-6 text-muted-foreground text-sm">
        No organization context found. Please log in again.
      </div>
    )
  }

  return (
    <div>
      <PageHeader
        title="Identity Providers"
        description="Connect your organization to an external OIDC identity provider"
        action={
          isOrgAdmin ? (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              Add Provider
            </Button>
          ) : undefined
        }
      />

      {!isOrgAdmin && (
        <div className="mb-4 flex items-center gap-2 rounded-md border border-blue-500/30 bg-blue-500/10 px-4 py-3 text-sm text-blue-300">
          <span>You have read-only access. Only org admins can manage identity providers.</span>
        </div>
      )}

      <ResourceTable
        columns={columns as unknown as Column<Record<string, unknown>>[]}
        data={idps as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No identity providers configured. Add an OIDC provider to enable SSO login for your organization."
      />

      {isOrgAdmin && (
        <CreateIdpDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onSubmit={(req) => createMutation.mutate(req)}
          loading={createMutation.isPending}
        />
      )}

      {isOrgAdmin && editTarget && (
        <EditIdpDialog
          open
          idp={editTarget}
          onOpenChange={(open) => !open && setEditTarget(null)}
          onSubmit={(req) => updateMutation.mutate({ idpId: editTarget.id, req })}
          loading={updateMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Identity Provider"
        description={
          `Delete "${deleteTarget?.name}"? All federated identity links will be removed and users who log in via this provider will no longer be able to authenticate.`
        }
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

type IdpFormData = {
  name: string
  issuer_url: string
  client_id: string
  client_secret: string
  scopes: string
  claim_email: string
  claim_name: string
  auto_provision: boolean
  default_role: string
  domain_hint: string
}

function IdpForm({
  initial,
  onSubmit,
  loading,
  onCancel,
  isEdit = false,
}: {
  initial: IdpFormData
  onSubmit: (data: IdpFormData) => void
  loading: boolean
  onCancel: () => void
  isEdit?: boolean
}) {
  const [form, setForm] = useState<IdpFormData>(initial)

  function set(key: keyof IdpFormData, value: string | boolean) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit(form)
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div className="grid grid-cols-2 gap-4">
        <div className="col-span-2 space-y-2">
          <Label htmlFor="idp-name">Provider Name</Label>
          <Input
            id="idp-name"
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            placeholder="My SSO Provider"
            required
          />
        </div>
        <div className="col-span-2 space-y-2">
          <Label htmlFor="idp-issuer">Issuer URL</Label>
          <Input
            id="idp-issuer"
            value={form.issuer_url}
            onChange={(e) => set('issuer_url', e.target.value)}
            placeholder="https://accounts.example.com"
            required
          />
          <p className="text-xs text-muted-foreground">
            The OIDC issuer URL. A discovery document must be available at{' '}
            <span className="font-mono">{form.issuer_url || 'https://…'}/.well-known/openid-configuration</span>
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-client-id">Client ID</Label>
          <Input
            id="idp-client-id"
            value={form.client_id}
            onChange={(e) => set('client_id', e.target.value)}
            required
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-client-secret">Client Secret</Label>
          <Input
            id="idp-client-secret"
            type="password"
            value={form.client_secret}
            onChange={(e) => set('client_secret', e.target.value)}
            placeholder={isEdit ? '(leave blank to keep current)' : ''}
            required={!isEdit}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-scopes">Scopes</Label>
          <Input
            id="idp-scopes"
            value={form.scopes}
            onChange={(e) => set('scopes', e.target.value)}
            placeholder="openid email profile"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-domain-hint">Domain Hint</Label>
          <Input
            id="idp-domain-hint"
            value={form.domain_hint}
            onChange={(e) => set('domain_hint', e.target.value)}
            placeholder="example.com (optional)"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-claim-email">Email Claim</Label>
          <Input
            id="idp-claim-email"
            value={form.claim_email}
            onChange={(e) => set('claim_email', e.target.value)}
            placeholder="email"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="idp-claim-name">Name Claim</Label>
          <Input
            id="idp-claim-name"
            value={form.claim_name}
            onChange={(e) => set('claim_name', e.target.value)}
            placeholder="name"
          />
        </div>
        <div className="space-y-2">
          <Label>Default Role</Label>
          <Select value={form.default_role} onValueChange={(v) => set('default_role', v)}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="member">Member</SelectItem>
              <SelectItem value="org_admin">Org Admin</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="flex flex-col gap-1 justify-center">
          <Label>Auto-provision Users</Label>
          <label className="flex items-center gap-2 pt-1 cursor-pointer select-none">
            <input
              type="checkbox"
              className="accent-primary w-4 h-4"
              checked={form.auto_provision}
              onChange={(e) => set('auto_provision', e.target.checked)}
            />
            <span className="text-sm text-muted-foreground">
              {form.auto_provision ? 'Create user on first login' : 'Require manual invite'}
            </span>
          </label>
        </div>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel} disabled={loading}>
          Cancel
        </Button>
        <Button type="submit" disabled={loading}>
          {loading ? (isEdit ? 'Saving…' : 'Creating…') : (isEdit ? 'Save Changes' : 'Add Provider')}
        </Button>
      </DialogFooter>
    </form>
  )
}

const defaultForm: IdpFormData = {
  name: '',
  issuer_url: '',
  client_id: '',
  client_secret: '',
  scopes: 'openid email profile',
  claim_email: 'email',
  claim_name: 'name',
  auto_provision: true,
  default_role: 'member',
  domain_hint: '',
}

function CreateIdpDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: IdpFormData) => void
  loading: boolean
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader><DialogTitle>Add Identity Provider</DialogTitle></DialogHeader>
        <IdpForm
          initial={{ ...defaultForm }}
          onSubmit={onSubmit}
          loading={loading}
          onCancel={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function EditIdpDialog({
  open,
  idp,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  idp: IdentityProvider
  onOpenChange: (open: boolean) => void
  onSubmit: (data: Partial<IdpFormData>) => void
  loading: boolean
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader><DialogTitle>Edit Provider — {idp.name}</DialogTitle></DialogHeader>
        <IdpForm
          initial={{
            name: idp.name,
            issuer_url: idp.issuer_url,
            client_id: idp.client_id,
            client_secret: '',
            scopes: idp.scopes,
            claim_email: idp.claim_email,
            claim_name: idp.claim_name,
            auto_provision: idp.auto_provision,
            default_role: idp.default_role,
            domain_hint: idp.domain_hint,
          }}
          onSubmit={onSubmit}
          loading={loading}
          onCancel={() => onOpenChange(false)}
          isEdit
        />
      </DialogContent>
    </Dialog>
  )
}
