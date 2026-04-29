import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Pencil } from 'lucide-react'
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
import { listProjects, createProject, updateProject, deleteProject } from '@/api/identity'
import { useAuthStore } from '@/store/auth'
import type { Project } from '@/types/identity'

export default function ProjectsPage() {
  const qc = useQueryClient()
  const isOrgAdmin = useAuthStore((s) => s.isOrgAdmin)()
  const [createOpen, setCreateOpen] = useState(false)
  const [renameTarget, setRenameTarget] = useState<Project | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null)

  const { data: projects = [], isLoading, error } = useQuery({
    queryKey: ['projects'],
    queryFn: () => listProjects(),
  })

  const createMutation = useMutation({
    mutationFn: createProject,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      setCreateOpen(false)
      toast({ title: 'Project created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const renameMutation = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => updateProject(id, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      setRenameTarget(null)
      toast({ title: 'Project renamed' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Rename failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteProject,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      setDeleteTarget(null)
      toast({ title: 'Project deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Project>[] = [
    { key: 'name', header: 'Name' },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    {
      key: 'created_at',
      header: 'Created',
      render: (r) => new Date(r.created_at).toLocaleString(),
    },
    ...(isOrgAdmin ? [{
      key: 'actions' as keyof Project,
      header: '',
      render: (row: Project) => (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            title="Rename project"
            onClick={(e) => { e.stopPropagation(); setRenameTarget(row) }}
          >
            <Pencil className="h-4 w-4 text-muted-foreground" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            title="Delete project"
            onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
          >
            <Trash2 className="h-4 w-4 text-muted-foreground" />
          </Button>
        </div>
      ),
    }] : []),
  ]

  return (
    <div>
      <PageHeader
        title="Projects"
        description="Tenant projects"
        action={
          isOrgAdmin ? (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              Create Project
            </Button>
          ) : undefined
        }
      />
      <ResourceTable
        columns={columns as unknown as Column<Record<string, unknown>>[]}
        data={projects as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No projects."
      />

      {isOrgAdmin && (
        <CreateProjectDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onSubmit={(name) => createMutation.mutate(name)}
          loading={createMutation.isPending}
        />
      )}

      {isOrgAdmin && renameTarget && (
        <RenameProjectDialog
          open
          project={renameTarget}
          onOpenChange={(open) => !open && setRenameTarget(null)}
          onSubmit={(name) => renameMutation.mutate({ id: renameTarget.id, name })}
          loading={renameMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Project"
        description={`Delete project "${deleteTarget?.name}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

function CreateProjectDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (name: string) => void
  loading: boolean
}) {
  const [name, setName] = useState('')

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit(name)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Create Project</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="proj-name">Name</Label>
            <Input
              id="proj-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-project"
              required
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>Cancel</Button>
            <Button type="submit" disabled={loading}>{loading ? 'Creating…' : 'Create'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Rename dialog ────────────────────────────────────────────────────────────

function RenameProjectDialog({
  open,
  project,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  project: Project
  onOpenChange: (open: boolean) => void
  onSubmit: (name: string) => void
  loading: boolean
}) {
  const [name, setName] = useState(project.name)

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit(name)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Rename Project</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="rename-proj-name">Name</Label>
            <Input
              id="rename-proj-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>Cancel</Button>
            <Button type="submit" disabled={loading || name === project.name}>{loading ? 'Saving…' : 'Rename'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
