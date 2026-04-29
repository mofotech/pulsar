import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, Plus } from 'lucide-react'
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
import { listVolumeTypes, createVolumeType, deleteVolumeType } from '@/api/storage'
import type { VolumeType } from '@/types/storage'

const DRIVERS = ['lvm', 'ceph', 'nfs', 'glusterfs']

export default function VolumeTypesPage() {
  const qc = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<VolumeType | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  const { data: types = [], isLoading, error } = useQuery({
    queryKey: ['volume-types'],
    queryFn: listVolumeTypes,
  })

  const deleteMutation = useMutation({
    mutationFn: deleteVolumeType,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['volume-types'] })
      setDeleteTarget(null)
      toast({ title: 'Volume type deleted' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Delete failed', description: err.message }),
  })

  const columns: Column<VolumeType>[] = [
    { key: 'name', header: 'Name' },
    {
      key: 'driver',
      header: 'Driver',
      render: (r) => (
        <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium dark:bg-slate-800">
          {r.driver}
        </span>
      ),
    },
    { key: 'created_at', header: 'Created', render: (r) => new Date(r.created_at).toLocaleString() },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <Button variant="ghost" size="icon" onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}>
          <Trash2 className="h-4 w-4 text-muted-foreground" />
        </Button>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Volume Types"
        description="Storage backend types"
        action={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" /> Create Type
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={types as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No volume types. Create one to start provisioning volumes."
      />

      <CreateVolumeTypeDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={() => { qc.invalidateQueries({ queryKey: ['volume-types'] }); setCreateOpen(false) }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Volume Type"
        description={`Delete volume type "${deleteTarget?.name}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

function CreateVolumeTypeDialog({
  open, onOpenChange, onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [driver, setDriver] = useState('lvm')

  const mutation = useMutation({
    mutationFn: () => createVolumeType({ name, driver }),
    onSuccess: () => {
      toast({ title: 'Volume type created' })
      setName(''); setDriver('lvm')
      onCreated()
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Create failed', description: err.message }),
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xs">
        <DialogHeader><DialogTitle>Create Volume Type</DialogTitle></DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input placeholder="standard-lvm" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>Driver</Label>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={driver}
              onChange={(e) => setDriver(e.target.value)}
            >
              {DRIVERS.map((d) => <option key={d} value={d}>{d}</option>)}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={mutation.isPending}>Cancel</Button>
          <Button onClick={() => mutation.mutate()} disabled={!name || mutation.isPending}>
            {mutation.isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
