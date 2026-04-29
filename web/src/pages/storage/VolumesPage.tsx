import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, Plus, Plug, Unplug, ArrowUpCircle, Gauge } from 'lucide-react'
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
import { listVolumes, createVolume, deleteVolume, volumeAction } from '@/api/storage'
import { listVolumeTypes } from '@/api/storage'
import { listInstances } from '@/api/compute'
import { listImages } from '@/api/images'
import type { Volume, VolumeActionRequest } from '@/types/storage'

export default function VolumesPage() {
  const qc = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<Volume | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [actionTarget, setActionTarget] = useState<{ vol: Volume; action: 'attach' | 'detach' | 'extend' | 'set-qos' } | null>(null)

  const { data: volumes = [], isLoading, error } = useQuery({
    queryKey: ['volumes'],
    queryFn: listVolumes,
    refetchInterval: 5_000,
  })
  const { data: volumeTypes = [] } = useQuery({ queryKey: ['volume-types'], queryFn: listVolumeTypes })
  const { data: instances = [] } = useQuery({ queryKey: ['instances'], queryFn: listInstances })

  const deleteMutation = useMutation({
    mutationFn: deleteVolume,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['volumes'] })
      setDeleteTarget(null)
      toast({ title: 'Volume deleted' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Delete failed', description: err.message }),
  })

  const actionMutation = useMutation({
    mutationFn: ({ id, req }: { id: string; req: VolumeActionRequest }) => volumeAction(id, req),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['volumes'] })
      setActionTarget(null)
      toast({ title: `${vars.req.action} succeeded` })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Action failed', description: err.message }),
  })

  const columns: Column<Volume>[] = [
    { key: 'name', header: 'Name' },
    { key: 'size_gb', header: 'Size', render: (r) => `${r.size_gb} GB` },
    {
      key: 'volume_type_id',
      header: 'Type',
      render: (r) => {
        const vt = volumeTypes.find((t) => t.id === r.volume_type_id)
        return vt ? vt.name : (r.volume_type_id ? r.volume_type_id.slice(0, 8) : '—')
      },
    },
    {
      key: 'status',
      header: 'Status',
      render: (r) => (
        <div className="flex items-center gap-1.5">
          <StatusBadge status={(r as unknown as Volume).status} />
          {(r as unknown as Volume).bootable && (
            <span className="rounded bg-blue-500/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-blue-400">boot</span>
          )}
        </div>
      ),
    },
    {
      key: 'attached_to',
      header: 'Attached To',
      render: (r) => {
        if (!r.attached_to) return '—'
        const inst = instances.find((i: { id: string; name: string }) => i.id === r.attached_to)
        return (
          <span className="text-sm">
            {inst ? inst.name : r.attached_to.slice(0, 8)}
            {r.device_path && <span className="ml-1 font-mono text-xs text-muted-foreground">{r.device_path}</span>}
          </span>
        )
      },
    },
    { key: 'created_at', header: 'Created', render: (r) => new Date(r.created_at).toLocaleString() },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <div className="flex items-center gap-1">
          {row.status === 'available' && (
            <Button variant="ghost" size="icon" title="Attach"
              onClick={(e) => { e.stopPropagation(); setActionTarget({ vol: row, action: 'attach' }) }}>
              <Plug className="h-4 w-4 text-muted-foreground" />
            </Button>
          )}
          {row.status === 'in-use' && (
            <Button variant="ghost" size="icon" title="Detach"
              onClick={(e) => { e.stopPropagation(); setActionTarget({ vol: row, action: 'detach' }) }}>
              <Unplug className="h-4 w-4 text-muted-foreground" />
            </Button>
          )}
          {(row.status === 'available' || row.status === 'in-use') && (
            <Button variant="ghost" size="icon" title="Extend"
              onClick={(e) => { e.stopPropagation(); setActionTarget({ vol: row, action: 'extend' }) }}>
              <ArrowUpCircle className="h-4 w-4 text-muted-foreground" />
            </Button>
          )}
          {(row.status === 'available' || row.status === 'in-use') && (
            <Button variant="ghost" size="icon" title="Set QoS"
              onClick={(e) => { e.stopPropagation(); setActionTarget({ vol: row, action: 'set-qos' }) }}>
              <Gauge className="h-4 w-4 text-muted-foreground" />
            </Button>
          )}
          <Button variant="ghost" size="icon" title="Delete"
            onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}>
            <Trash2 className="h-4 w-4 text-muted-foreground" />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Volumes"
        description="Block storage volumes"
        action={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" /> Create Volume
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={volumes as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No volumes. Create one to get started."
      />

      {/* Create */}
      <CreateVolumeDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        volumeTypes={volumeTypes}
        onCreated={() => { qc.invalidateQueries({ queryKey: ['volumes'] }); setCreateOpen(false) }}
      />

      {/* Action dialogs */}
      {actionTarget?.action === 'attach' && (
        <AttachDialog
          open
          vol={actionTarget.vol}
          instances={instances}
          onOpenChange={(open) => !open && setActionTarget(null)}
          onConfirm={(req) => actionMutation.mutate({ id: actionTarget.vol.id, req })}
          saving={actionMutation.isPending}
        />
      )}
      {actionTarget?.action === 'detach' && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setActionTarget(null)}
          title="Detach Volume"
          description={`Detach "${actionTarget.vol.name}" from its instance? The volume will be made available again.`}
          onConfirm={() => actionMutation.mutate({ id: actionTarget.vol.id, req: { action: 'detach' } })}
          loading={actionMutation.isPending}
        />
      )}
      {actionTarget?.action === 'extend' && (
        <ExtendDialog
          open
          vol={actionTarget.vol}
          onOpenChange={(open) => !open && setActionTarget(null)}
          onConfirm={(newSize) => actionMutation.mutate({ id: actionTarget.vol.id, req: { action: 'extend', new_size_gb: newSize } })}
          saving={actionMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Volume"
        description={`Delete volume "${deleteTarget?.name}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create dialog ────────────────────────────────────────────────────────────

function CreateVolumeDialog({
  open, onOpenChange, volumeTypes, onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  volumeTypes: { id: string; name: string; driver: string }[]
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [sizeGB, setSizeGB] = useState('10')
  const [vtID, setVtID] = useState('')
  const [imageID, setImageID] = useState('') // optional: make bootable from image

  const { data: images = [] } = useQuery({ queryKey: ['images'], queryFn: listImages })

  const mutation = useMutation({
    mutationFn: () => createVolume({
      name,
      size_gb: parseInt(sizeGB, 10),
      volume_type_id: vtID || undefined,
      image_id: imageID || undefined,
    }),
    onSuccess: () => {
      toast({ title: imageID ? 'Bootable volume creation started' : 'Volume created' })
      setName(''); setSizeGB('10'); setVtID(''); setImageID('')
      onCreated()
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Create failed', description: err.message }),
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Create Volume</DialogTitle></DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input placeholder="my-volume" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>Size (GB)</Label>
            <Input type="number" min={1} value={sizeGB} onChange={(e) => setSizeGB(e.target.value)} />
          </div>
          {volumeTypes.length > 0 && (
            <div className="space-y-1.5">
              <Label>Volume Type</Label>
              <select
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                value={vtID}
                onChange={(e) => setVtID(e.target.value)}
              >
                <option value="">— none —</option>
                {volumeTypes.map((vt) => (
                  <option key={vt.id} value={vt.id}>{vt.name} ({vt.driver})</option>
                ))}
              </select>
            </div>
          )}
          <div className="space-y-1.5">
            <Label>
              Source Image <span className="text-muted-foreground font-normal text-xs">(optional — creates a bootable volume)</span>
            </Label>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={imageID}
              onChange={(e) => setImageID(e.target.value)}
            >
              <option value="">— none (empty volume) —</option>
              {(images as Array<{ id: string; name: string }>).map((img) => (
                <option key={img.id} value={img.id}>{img.name}</option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={mutation.isPending}>Cancel</Button>
          <Button onClick={() => mutation.mutate()} disabled={!name || !sizeGB || mutation.isPending}>
            {mutation.isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Attach dialog ────────────────────────────────────────────────────────────

function AttachDialog({
  open, vol, instances, onOpenChange, onConfirm, saving,
}: {
  open: boolean
  vol: Volume
  instances: { id: string; name: string; status: string }[]
  onOpenChange: (open: boolean) => void
  onConfirm: (req: VolumeActionRequest) => void
  saving: boolean
}) {
  const [instanceID, setInstanceID] = useState('')
  const [devicePath, setDevicePath] = useState('/dev/vdb')

  const activeInstances = instances.filter((i) => i.status === 'active')

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Attach Volume — {vol.name}</DialogTitle></DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label>Instance</Label>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={instanceID}
              onChange={(e) => setInstanceID(e.target.value)}
            >
              <option value="">Select instance…</option>
              {activeInstances.map((i) => (
                <option key={i.id} value={i.id}>{i.name}</option>
              ))}
            </select>
            {activeInstances.length === 0 && (
              <p className="text-xs text-muted-foreground">No active instances available.</p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label>Guest Device Path</Label>
            <Input
              placeholder="/dev/vdb"
              value={devicePath}
              onChange={(e) => setDevicePath(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">The device name as it will appear inside the instance.</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button
            onClick={() => onConfirm({ action: 'attach', instance_id: instanceID, device_path: devicePath })}
            disabled={!instanceID || !devicePath || saving}
          >
            {saving ? 'Attaching…' : 'Attach'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Extend dialog ────────────────────────────────────────────────────────────

function ExtendDialog({
  open, vol, onOpenChange, onConfirm, saving,
}: {
  open: boolean
  vol: Volume
  onOpenChange: (open: boolean) => void
  onConfirm: (newSize: number) => void
  saving: boolean
}) {
  const [newSize, setNewSize] = useState(String(vol.size_gb + 10))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xs">
        <DialogHeader><DialogTitle>Extend Volume — {vol.name}</DialogTitle></DialogHeader>
        <div className="space-y-4 py-1">
          <p className="text-sm text-muted-foreground">Current size: <strong>{vol.size_gb} GB</strong></p>
          <div className="space-y-1.5">
            <Label>New Size (GB)</Label>
            <Input
              type="number"
              min={vol.size_gb + 1}
              value={newSize}
              onChange={(e) => setNewSize(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button
            onClick={() => onConfirm(parseInt(newSize, 10))}
            disabled={parseInt(newSize, 10) <= vol.size_gb || saving}
          >
            {saving ? 'Extending…' : 'Extend'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
