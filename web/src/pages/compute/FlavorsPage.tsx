import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
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
import { listFlavors, createFlavor, deleteFlavor } from '@/api/compute'
import type { Flavor } from '@/types/compute'

export default function FlavorsPage() {
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Flavor | null>(null)

  const { data: flavors = [], isLoading, error } = useQuery({
    queryKey: ['flavors'],
    queryFn: listFlavors,
  })

  const createMutation = useMutation({
    mutationFn: createFlavor,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['flavors'] })
      setCreateOpen(false)
      toast({ title: 'Flavor created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteFlavor,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['flavors'] })
      setDeleteTarget(null)
      toast({ title: 'Flavor deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Flavor>[] = [
    { key: 'name', header: 'Name' },
    { key: 'vcpus', header: 'VCPUs', render: (r) => String(r.vcpus) },
    { key: 'ram_mb', header: 'RAM (MB)', render: (r) => String(r.ram_mb) },
    { key: 'disk_gb', header: 'Disk (GB)', render: (r) => String(r.disk_gb) },
    { key: 'ephemeral_gb', header: 'Ephemeral (GB)', render: (r) => String(r.ephemeral_gb) },
    {
      key: 'public',
      header: 'Public',
      render: (r) => (r.public ? 'Yes' : 'No'),
    },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <Button
          variant="ghost"
          size="icon"
          onClick={(e) => {
            e.stopPropagation()
            setDeleteTarget(row)
          }}
        >
          <Trash2 className="h-4 w-4 text-muted-foreground" />
        </Button>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Flavors"
        description="Compute flavor definitions"
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            Create Flavor
          </Button>
        }
      />

      <ResourceTable
        columns={columns}
        data={flavors as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No flavors defined."
      />

      <CreateFlavorDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Flavor"
        description={`Delete flavor "${deleteTarget?.name}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

interface FlavorForm {
  name: string
  vcpus: string
  ram_mb: string
  disk_gb: string
  ephemeral_gb: string
  public: boolean
}

function CreateFlavorDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: Omit<Flavor, 'id'>) => void
  loading: boolean
}) {
  const [form, setForm] = useState<FlavorForm>({
    name: '',
    vcpus: '2',
    ram_mb: '2048',
    disk_gb: '20',
    ephemeral_gb: '0',
    public: true,
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({
      name: form.name,
      vcpus: parseInt(form.vcpus),
      ram_mb: parseInt(form.ram_mb),
      disk_gb: parseInt(form.disk_gb),
      ephemeral_gb: parseInt(form.ephemeral_gb),
      public: form.public,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Flavor</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="flavor-name">Name</Label>
            <Input
              id="flavor-name"
              value={form.name}
              onChange={(e) => setForm((p) => ({ ...p, name: e.target.value }))}
              placeholder="m1.small"
              required
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="flavor-vcpus">VCPUs</Label>
              <Input
                id="flavor-vcpus"
                type="number"
                min="1"
                value={form.vcpus}
                onChange={(e) => setForm((p) => ({ ...p, vcpus: e.target.value }))}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="flavor-ram">RAM (MB)</Label>
              <Input
                id="flavor-ram"
                type="number"
                min="128"
                value={form.ram_mb}
                onChange={(e) => setForm((p) => ({ ...p, ram_mb: e.target.value }))}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="flavor-disk">Disk (GB)</Label>
              <Input
                id="flavor-disk"
                type="number"
                min="1"
                value={form.disk_gb}
                onChange={(e) => setForm((p) => ({ ...p, disk_gb: e.target.value }))}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="flavor-eph">Ephemeral (GB)</Label>
              <Input
                id="flavor-eph"
                type="number"
                min="0"
                value={form.ephemeral_gb}
                onChange={(e) => setForm((p) => ({ ...p, ephemeral_gb: e.target.value }))}
                required
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              id="flavor-public"
              type="checkbox"
              checked={form.public}
              onChange={(e) => setForm((p) => ({ ...p, public: e.target.checked }))}
              className="h-4 w-4 rounded border-input"
            />
            <Label htmlFor="flavor-public">Public</Label>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? 'Creating...' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
