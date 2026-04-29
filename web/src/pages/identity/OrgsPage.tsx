import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Pencil, Trash2, Building2, ShieldCheck, Lock } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { StatusBadge } from '@/components/common/StatusBadge'
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
import { listOrgs, createOrg, updateOrg, deleteOrg } from '@/api/identity'
import { useAuthStore } from '@/store/auth'
import type { Organization } from '@/types/identity'

export default function OrgsPage() {
  const qc = useQueryClient()
  const isPlatformAdmin = useAuthStore((s) => s.isPlatformAdmin)()

  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Organization | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Organization | null>(null)

  const { data: orgs = [], isLoading, error } = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  })

  const createMutation = useMutation({
    mutationFn: createOrg,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['orgs'] })
      setCreateOpen(false)
      toast({ title: 'Organization created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const editMutation = useMutation({
    mutationFn: ({ id, req }: { id: string; req: { name?: string; description?: string } }) =>
      updateOrg(id, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['orgs'] })
      setEditTarget(null)
      toast({ title: 'Organization updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteOrg,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['orgs'] })
      setDeleteTarget(null)
      toast({ title: 'Organization deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Organization>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (r) => (
        <div className="flex items-center gap-2">
          <Building2 className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="font-medium">{r.name}</span>
          {r.is_default && (
            <span className="inline-flex items-center gap-1 rounded bg-primary/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary">
              <ShieldCheck className="h-2.5 w-2.5" />
              default
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'slug',
      header: 'Slug',
      render: (r) => <span className="font-mono text-xs text-muted-foreground">{r.slug}</span>,
    },
    {
      key: 'description',
      header: 'Description',
      render: (r) => (
        <span className="text-sm text-muted-foreground">{r.description || '—'}</span>
      ),
    },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    {
      key: 'created_at',
      header: 'Created',
      render: (r) => new Date(r.created_at).toLocaleString(),
    },
    ...(isPlatformAdmin
      ? [
          {
            key: 'actions' as keyof Organization,
            header: '',
            render: (row: Organization) => (
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="icon"
                  title={row.is_default ? 'Cannot edit the default org' : 'Edit organization'}
                  disabled={row.is_default}
                  onClick={(e) => { e.stopPropagation(); setEditTarget(row) }}
                >
                  {row.is_default
                    ? <Lock className="h-4 w-4 text-muted-foreground/40" />
                    : <Pencil className="h-4 w-4 text-muted-foreground" />}
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  title={row.is_default ? 'Cannot delete the default org' : 'Delete organization'}
                  disabled={row.is_default}
                  onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
                >
                  <Trash2 className={`h-4 w-4 ${row.is_default ? 'text-muted-foreground/40' : 'text-muted-foreground'}`} />
                </Button>
              </div>
            ),
          },
        ]
      : []),
  ]

  return (
    <div>
      <PageHeader
        title="Organizations"
        description="Tenant organizations managed by the Pulsar service provider"
        action={
          isPlatformAdmin ? (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              Create Organization
            </Button>
          ) : undefined
        }
      />

      {!isPlatformAdmin && (
        <div className="mb-6 flex items-center gap-2 rounded-lg border border-border/60 bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
          <Lock className="h-4 w-4 shrink-0" />
          Organization management is restricted to platform administrators.
        </div>
      )}

      <ResourceTable
        columns={columns as unknown as Column<Record<string, unknown>>[]}
        data={orgs as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No organizations."
      />

      {isPlatformAdmin && (
        <CreateOrgDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onSubmit={(req) => createMutation.mutate(req)}
          loading={createMutation.isPending}
        />
      )}

      {isPlatformAdmin && editTarget && (
        <EditOrgDialog
          open
          org={editTarget}
          onOpenChange={(open) => !open && setEditTarget(null)}
          onSubmit={(req) => editMutation.mutate({ id: editTarget.id, req })}
          loading={editMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Organization"
        description={`Delete organization "${deleteTarget?.name}"? All projects and users belonging to this organization will be permanently removed. This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

function CreateOrgDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { name: string; slug: string; description: string }) => void
  loading: boolean
}) {
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [description, setDescription] = useState('')
  const [slugTouched, setSlugTouched] = useState(false)

  // Auto-derive slug from name while the user hasn't manually edited it
  function handleNameChange(value: string) {
    setName(value)
    if (!slugTouched) {
      setSlug(value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, ''))
    }
  }

  function handleSlugChange(value: string) {
    setSlugTouched(true)
    setSlug(value.toLowerCase().replace(/[^a-z0-9-]/g, ''))
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({ name, slug, description })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Organization</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="org-name">Name</Label>
            <Input
              id="org-name"
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder="Acme Corp"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="org-slug">
              Slug
              <span className="ml-1 text-xs text-muted-foreground">(URL-safe identifier)</span>
            </Label>
            <Input
              id="org-slug"
              value={slug}
              onChange={(e) => handleSlugChange(e.target.value)}
              placeholder="acme-corp"
              pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]"
              required
            />
            <p className="text-xs text-muted-foreground">
              Lowercase letters, numbers, and hyphens only.
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="org-description">
              Description
              <span className="ml-1 text-xs text-muted-foreground">(optional)</span>
            </Label>
            <Input
              id="org-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Customer-facing cloud tenant"
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading || !name || !slug}>
              {loading ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Edit dialog ──────────────────────────────────────────────────────────────

function EditOrgDialog({
  open,
  org,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  org: Organization
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { name?: string; description?: string }) => void
  loading: boolean
}) {
  const [name, setName] = useState(org.name)
  const [description, setDescription] = useState(org.description)

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const req: { name?: string; description?: string } = {}
    if (name !== org.name) req.name = name
    if (description !== org.description) req.description = description
    onSubmit(req)
  }

  const unchanged = name === org.name && description === org.description

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Edit Organization</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="edit-org-name">Name</Label>
            <Input
              id="edit-org-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-org-slug">Slug</Label>
            <Input
              id="edit-org-slug"
              value={org.slug}
              disabled
              className="opacity-50 cursor-not-allowed"
            />
            <p className="text-xs text-muted-foreground">
              Slug cannot be changed after creation.
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-org-description">Description</Label>
            <Input
              id="edit-org-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Customer-facing cloud tenant"
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading || unchanged}>
              {loading ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
