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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from '@/hooks/use-toast'
import { listSubnets, createSubnet, deleteSubnet, listNetworks } from '@/api/network'
import type { Subnet, Network } from '@/types/network'

export default function SubnetsPage() {
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Subnet | null>(null)

  const { data: subnets = [], isLoading, error } = useQuery({
    queryKey: ['subnets'],
    queryFn: listSubnets,
  })

  const { data: networks = [] } = useQuery({
    queryKey: ['networks'],
    queryFn: listNetworks,
  })

  const networkMap = Object.fromEntries((networks as Network[]).map((n) => [n.id, n.name]))

  const createMutation = useMutation({
    mutationFn: createSubnet,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['subnets'] })
      setCreateOpen(false)
      toast({ title: 'Subnet created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteSubnet,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['subnets'] })
      setDeleteTarget(null)
      toast({ title: 'Subnet deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Subnet>[] = [
    { key: 'name', header: 'Name', render: (r) => r.name ?? '—' },
    {
      key: 'network_id',
      header: 'Network',
      render: (r) => networkMap[r.network_id]
        ? <span>{networkMap[r.network_id]}</span>
        : <span className="font-mono text-xs text-muted-foreground">{r.network_id.slice(0, 8)}…</span>,
    },
    { key: 'cidr', header: 'CIDR' },
    { key: 'gateway_ip', header: 'Gateway IP', render: (r) => r.gateway_ip ?? '—' },
    {
      key: 'created_at',
      header: 'Created',
      render: (r) => new Date(r.created_at).toLocaleString(),
    },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <Button
          variant="ghost"
          size="icon"
          onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
        >
          <Trash2 className="h-4 w-4 text-muted-foreground" />
        </Button>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Subnets"
        description="Network subnets"
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            Create Subnet
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={subnets as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No subnets."
      />
      <CreateSubnetDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        networks={networks as Network[]}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Subnet"
        description={`Delete subnet "${deleteTarget?.name ?? deleteTarget?.cidr}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

function CreateSubnetDialog({
  open,
  onOpenChange,
  networks,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  networks: Network[]
  onSubmit: (data: Partial<Subnet>) => void
  loading: boolean
}) {
  const [form, setForm] = useState({ name: '', network_id: '', cidr: '', gateway_ip: '' })
  const set = (k: keyof typeof form, v: string) => setForm((p) => ({ ...p, [k]: v }))

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({
      name: form.name || undefined,
      network_id: form.network_id,
      cidr: form.cidr,
      gateway_ip: form.gateway_ip || undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>Create Subnet</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label>Name <span className="text-muted-foreground">(optional)</span></Label>
            <Input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="my-subnet" />
          </div>
          <div className="space-y-2">
            <Label>Network</Label>
            <Select value={form.network_id} onValueChange={(v) => set('network_id', v)} required>
              <SelectTrigger>
                <SelectValue placeholder="Select a network…" />
              </SelectTrigger>
              <SelectContent>
                {networks.length === 0 && (
                  <div className="px-2 py-3 text-center text-xs text-muted-foreground">
                    No networks available
                  </div>
                )}
                {networks.map((n) => (
                  <SelectItem key={n.id} value={n.id}>
                    {n.name}
                    {n.external && (
                      <span className="ml-2 text-xs text-sky-400">(external)</span>
                    )}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>CIDR</Label>
            <Input value={form.cidr} onChange={(e) => set('cidr', e.target.value)} placeholder="10.0.0.0/24" required />
          </div>
          <div className="space-y-2">
            <Label>Gateway IP <span className="text-muted-foreground">(optional, auto-detected)</span></Label>
            <Input value={form.gateway_ip} onChange={(e) => set('gateway_ip', e.target.value)} placeholder="10.0.0.1" />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>Cancel</Button>
            <Button type="submit" disabled={loading || !form.network_id || !form.cidr}>
              {loading ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

