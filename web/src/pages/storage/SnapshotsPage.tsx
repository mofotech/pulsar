import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, Plus } from 'lucide-react'
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
import { listSnapshots, createSnapshot, deleteSnapshot, listVolumes } from '@/api/storage'
import type { Snapshot } from '@/types/storage'

export default function SnapshotsPage() {
  const qc = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<Snapshot | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  const { data: snapshots = [], isLoading, error } = useQuery({
    queryKey: ['snapshots'],
    queryFn: listSnapshots,
    refetchInterval: 5_000,
  })
  const { data: volumes = [] } = useQuery({ queryKey: ['volumes'], queryFn: listVolumes })

  const deleteMutation = useMutation({
    mutationFn: deleteSnapshot,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['snapshots'] })
      setDeleteTarget(null)
      toast({ title: 'Snapshot deleted' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Delete failed', description: err.message }),
  })

  const columns: Column<Snapshot>[] = [
    { key: 'name', header: 'Name' },
    {
      key: 'volume_id',
      header: 'Volume',
      render: (r) => {
        const vol = volumes.find((v) => v.id === r.volume_id)
        return vol ? vol.name : r.volume_id.slice(0, 8)
      },
    },
    { key: 'size_gb', header: 'Size', render: (r) => `${r.size_gb} GB` },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
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
        title="Snapshots"
        description="Volume snapshots"
        action={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" /> Create Snapshot
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={snapshots}
        isLoading={isLoading}
        error={error}
        emptyMessage="No snapshots."
      />

      <CreateSnapshotDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        volumes={volumes}
        onCreated={() => { qc.invalidateQueries({ queryKey: ['snapshots'] }); setCreateOpen(false) }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Snapshot"
        description={`Delete snapshot "${deleteTarget?.name}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

function CreateSnapshotDialog({
  open, onOpenChange, volumes, onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  volumes: { id: string; name: string; status: string }[]
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [volumeID, setVolumeID] = useState('')

  const snappableVolumes = volumes.filter((v) => v.status === 'available' || v.status === 'in-use')

  const mutation = useMutation({
    mutationFn: () => createSnapshot({ volume_id: volumeID, name }),
    onSuccess: () => {
      toast({ title: 'Snapshot creation started' })
      setName(''); setVolumeID('')
      onCreated()
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Create failed', description: err.message }),
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Create Snapshot</DialogTitle></DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label>Volume</Label>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={volumeID}
              onChange={(e) => setVolumeID(e.target.value)}
            >
              <option value="">Select volume…</option>
              {snappableVolumes.map((v) => (
                <option key={v.id} value={v.id}>{v.name}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label>Snapshot Name</Label>
            <Input placeholder="snap-before-upgrade" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={mutation.isPending}>Cancel</Button>
          <Button onClick={() => mutation.mutate()} disabled={!volumeID || !name || mutation.isPending}>
            {mutation.isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
