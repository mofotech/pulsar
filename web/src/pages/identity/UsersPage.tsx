import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Pencil, Trash2, ShieldCheck, ShieldOff } from 'lucide-react'
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
import { listUsers, createUser, updateUser, deleteUser, listProjects, listOrgs, listUserProjects, grantProjectAccess, revokeProjectAccess } from '@/api/identity'
import { useAuthStore } from '@/store/auth'
import type { User, Project, Organization } from '@/types/identity'

export default function UsersPage() {
  const qc = useQueryClient()
  const isAdmin = useAuthStore((s) => s.isAdmin)()
  const isPlatformAdmin = useAuthStore((s) => s.isPlatformAdmin)()
  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<User | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)

  const { data: users = [], isLoading, error } = useQuery({
    queryKey: ['users'],
    queryFn: listUsers,
  })

  const createMutation = useMutation({
    mutationFn: createUser,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      setCreateOpen(false)
      toast({ title: 'User created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, req }: { id: string; req: { role?: string; password?: string } }) =>
      updateUser(id, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      setEditTarget(null)
      toast({ title: 'User updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteUser,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      setDeleteTarget(null)
      toast({ title: 'User deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<User>[] = [
    { key: 'email', header: 'Email' },
    {
      key: 'role',
      header: 'Role',
      render: (r) => (
        <span className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-semibold ${
          r.role === 'admin'
            ? 'bg-amber-500/15 text-amber-400'
            : 'bg-muted text-muted-foreground'
        }`}>
          {r.role === 'admin'
            ? <ShieldCheck className="h-3 w-3" />
            : <ShieldOff className="h-3 w-3" />}
          {r.role}
        </span>
      ),
    },
    { key: 'project_id', header: 'Project ID', render: (r) => <span className="font-mono text-xs">{r.project_id.slice(0, 8)}…</span> },
    { key: 'created_at', header: 'Created', render: (r) => new Date(r.created_at).toLocaleString() },
    ...(isAdmin ? [{
      key: 'actions' as keyof User,
      header: '',
      render: (row: User) => {
        const isLastAdmin =
          row.role === 'admin' &&
          (users as User[]).filter((u) => u.role === 'admin').length === 1
        return (
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              title="Edit user"
              onClick={(e) => { e.stopPropagation(); setEditTarget(row) }}
            >
              <Pencil className="h-4 w-4 text-muted-foreground" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              title={isLastAdmin ? 'Cannot delete the last admin' : 'Delete user'}
              disabled={isLastAdmin}
              onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
            >
              <Trash2 className="h-4 w-4 text-muted-foreground" />
            </Button>
          </div>
        )
      },
    }] : []),
  ]

  return (
    <div>
      <PageHeader
        title="Users"
        description="Platform users"
        action={
          isAdmin ? (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              Create User
            </Button>
          ) : undefined
        }
      />
      <ResourceTable
        columns={columns}
        data={users}
        isLoading={isLoading}
        error={error}
        emptyMessage="No users."
      />

      {isAdmin && (
        <CreateUserDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onSubmit={(data) => createMutation.mutate(data)}
          loading={createMutation.isPending}
          canAssignOrgAdmin={isPlatformAdmin}
          canChooseOrg={isPlatformAdmin}
        />
      )}

      {isAdmin && editTarget && (
        <EditUserDialog
          open
          user={editTarget}
          isLastAdmin={
            editTarget.role === 'admin' &&
            (users as User[]).filter((u) => u.role === 'admin').length === 1
          }
          onOpenChange={(open) => !open && setEditTarget(null)}
          onSubmit={(req) => updateMutation.mutate({ id: editTarget.id, req })}
          loading={updateMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete User"
        description={`Delete user "${deleteTarget?.email}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

function CreateUserDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
  canAssignOrgAdmin = false,
  canChooseOrg = false,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: { email: string; password: string; role: string; org_role?: string; org_id?: string; project_id: string }) => void
  loading: boolean
  canAssignOrgAdmin?: boolean
  canChooseOrg?: boolean
}) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('member')
  const [orgRole, setOrgRole] = useState('member')
  const [orgId, setOrgId] = useState('')
  const [projectId, setProjectId] = useState('')

  const { data: orgs = [] } = useQuery<Organization[]>({
    queryKey: ['orgs'],
    queryFn: listOrgs,
    enabled: canChooseOrg,
  })

  // Fetch projects scoped to the selected org (or the caller's own org if none chosen).
  // Passing orgId to the query key means React Query refetches automatically when the
  // org selection changes, so the project dropdown always shows only valid choices.
  const { data: allProjects = [] } = useQuery<Project[]>({
    queryKey: ['projects', orgId || null],
    queryFn: () => listProjects(orgId || undefined),
  })

  // Reset the chosen project whenever the org selection changes.
  useEffect(() => { setProjectId('') }, [orgId])

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({
      email,
      password,
      role,
      project_id: projectId,
      ...(canAssignOrgAdmin && orgRole !== 'member' ? { org_role: orgRole } : {}),
      ...(canChooseOrg && orgId ? { org_id: orgId } : {}),
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>Create User</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          {canChooseOrg && (
            <div className="space-y-2">
              <Label>Organization</Label>
              <Select value={orgId} onValueChange={setOrgId}>
                <SelectTrigger>
                  <SelectValue placeholder="Default (your org)" />
                </SelectTrigger>
                <SelectContent>
                  {orgs.map((o) => (
                    <SelectItem key={o.id} value={o.id}>{o.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="space-y-2">
            <Label htmlFor="user-email">Email</Label>
            <Input
              id="user-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="user@example.com"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="user-password">Password</Label>
            <Input
              id="user-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••"
              required
            />
          </div>
          <div className="space-y-2">
            <Label>Role</Label>
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="admin">Admin</SelectItem>
                <SelectItem value="member">Member</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {canAssignOrgAdmin && (
            <div className="space-y-2">
              <Label>Org Role</Label>
              <Select value={orgRole} onValueChange={setOrgRole}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="member">Member</SelectItem>
                  <SelectItem value="org_admin">Org Admin</SelectItem>
                </SelectContent>
              </Select>
              {orgRole === 'org_admin' && (
                <p className="text-xs text-muted-foreground">
                  Org admins can manage identity providers and users within their organization.
                </p>
              )}
            </div>
          )}
          <div className="space-y-2">
            <Label>
              Project{' '}
              <span className="text-muted-foreground text-xs">(optional — can be assigned later)</span>
            </Label>
            <Select value={projectId} onValueChange={setProjectId}>
              <SelectTrigger>
                <SelectValue placeholder={allProjects.length === 0 ? 'No projects in this org yet' : 'Select a project…'} />
              </SelectTrigger>
              <SelectContent>
                {allProjects.map((p) => (
                  <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                ))}
                {allProjects.length === 0 && (
                  <div className="px-3 py-2 text-xs text-muted-foreground">
                    No projects yet — create one after adding the user.
                  </div>
                )}
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>{loading ? 'Creating…' : 'Create'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Edit dialog ──────────────────────────────────────────────────────────────

function EditUserDialog({
  open,
  user,
  isLastAdmin,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  user: User
  isLastAdmin: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { role?: string; password?: string }) => void
  loading: boolean
}) {
  const qc = useQueryClient()
  const [role, setRole] = useState(user.role)
  const [password, setPassword] = useState('')

  // Track which project IDs the user currently has access to
  const [memberOf, setMemberOf] = useState<Set<string>>(new Set())

  // Fetch all projects and this user's current projects in parallel
  const { data: allProjects = [] } = useQuery<Project[]>({
    queryKey: ['projects', null],
    queryFn: () => listProjects(),
    enabled: open,
  })
  const { data: userProjects = [] } = useQuery<Project[]>({
    queryKey: ['userProjects', user.id],
    queryFn: () => listUserProjects(user.id),
    enabled: open,
  })

  // Seed the membership state when data arrives
  useEffect(() => {
    if (userProjects.length > 0 || allProjects.length > 0) {
      const ids = new Set(userProjects.map((p) => p.id))
      setMemberOf(new Set(ids))
    }
  }, [userProjects, allProjects])

  const grantMutation = useMutation({
    mutationFn: ({ projectId }: { projectId: string }) =>
      grantProjectAccess(user.id, projectId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['userProjects', user.id] })
      toast({ title: 'Project access granted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Grant failed', description: err.message })
    },
  })

  const revokeMutation = useMutation({
    mutationFn: ({ projectId }: { projectId: string }) =>
      revokeProjectAccess(user.id, projectId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['userProjects', user.id] })
      toast({ title: 'Project access revoked' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Revoke failed', description: err.message })
    },
  })

  function toggleProject(projectId: string, checked: boolean) {
    if (checked) {
      grantMutation.mutate({ projectId })
      setMemberOf((prev) => new Set([...prev, projectId]))
    } else {
      revokeMutation.mutate({ projectId })
      setMemberOf((prev) => { const s = new Set(prev); s.delete(projectId); return s })
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const req: { role?: string; password?: string } = {}
    if (role !== user.role) req.role = role
    if (password) req.password = password
    onSubmit(req)
  }

  // Prevent demoting when this is the last admin.
  const wouldDemoteLastAdmin = isLastAdmin && role === 'member'

  const membershipBusy = grantMutation.isPending || revokeMutation.isPending

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>Edit User — {user.email}</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Role */}
          <div className="space-y-2">
            <Label>Role</Label>
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="admin">Admin</SelectItem>
                <SelectItem value="member" disabled={isLastAdmin}>
                  Member{isLastAdmin ? ' (last admin — cannot demote)' : ''}
                </SelectItem>
              </SelectContent>
            </Select>
            {wouldDemoteLastAdmin && (
              <p className="text-xs text-destructive">
                At least one admin must exist. Create another admin first.
              </p>
            )}
          </div>

          {/* Password */}
          <div className="space-y-2">
            <Label htmlFor="edit-password">
              New Password{' '}
              <span className="text-muted-foreground text-xs">(leave blank to keep current)</span>
            </Label>
            <Input
              id="edit-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••"
              autoComplete="new-password"
            />
          </div>

          {/* Project membership */}
          <div className="space-y-2">
            <Label>Project Access</Label>
            <div className="rounded-md border divide-y max-h-48 overflow-y-auto">
              {allProjects.map((p) => {
                const isHome = p.id === user.project_id
                const checked = memberOf.has(p.id)
                return (
                  <label
                    key={p.id}
                    className={`flex items-center gap-3 px-3 py-2 text-sm cursor-pointer select-none ${
                      isHome ? 'opacity-60' : 'hover:bg-muted/50'
                    }`}
                    title={isHome ? 'Home project — cannot revoke' : undefined}
                  >
                    <input
                      type="checkbox"
                      className="accent-primary"
                      checked={checked}
                      disabled={isHome || membershipBusy}
                      onChange={(e) => toggleProject(p.id, e.target.checked)}
                    />
                    <span className="flex-1">{p.name}</span>
                    {isHome && (
                      <span className="text-xs text-muted-foreground">home</span>
                    )}
                  </label>
                )
              })}
              {allProjects.length === 0 && (
                <p className="px-3 py-2 text-sm text-muted-foreground">Loading…</p>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading || wouldDemoteLastAdmin}>
              {loading ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
