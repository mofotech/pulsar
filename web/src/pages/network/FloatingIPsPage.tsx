import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Link, Unlink } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { toast } from '@/hooks/use-toast'
import {
  listFloatingIPs,
  createFloatingIP,
  updateFloatingIP,
  deleteFloatingIP,
  listPorts,
  listNetworks,
} from '@/api/network'
import { listInstances } from '@/api/compute'
import type { FloatingIP } from '@/types/network'

export default function FloatingIPsPage() {
  const qc = useQueryClient()
  const [associateTarget, setAssociateTarget] = useState<FloatingIP | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<FloatingIP | null>(null)

  const { data: fips = [], isLoading, error } = useQuery({
    queryKey: ['floatingips'],
    queryFn: listFloatingIPs,
  })

  const allocateMutation = useMutation({
    mutationFn: createFloatingIP,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['floatingips'] })
      toast({ title: 'Floating IP allocated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Allocate failed', description: err.message })
    },
  })

  const associateMutation = useMutation({
    mutationFn: ({ id, port_id }: { id: string; port_id: string }) =>
      updateFloatingIP(id, { port_id }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['floatingips'] })
      setAssociateTarget(null)
      toast({ title: 'Floating IP updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteFloatingIP,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['floatingips'] })
      setDeleteTarget(null)
      toast({ title: 'Floating IP released' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Release failed', description: err.message })
    },
  })

  const columns: Column<FloatingIP>[] = [
    { key: 'floating_ip_address', header: 'Floating IP' },
    { key: 'fixed_ip_address', header: 'Fixed IP', render: (r) => r.fixed_ip_address ?? '—' },
    { key: 'port_id', header: 'Port', render: (r) => r.port_id ? r.port_id.slice(0, 12) + '…' : '—' },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <div className="flex gap-1">
          {!row.port_id ? (
            <Button
              variant="ghost"
              size="icon"
              title="Associate"
              onClick={(e) => { e.stopPropagation(); setAssociateTarget(row) }}
            >
              <Link className="h-4 w-4 text-muted-foreground" />
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon"
              title="Disassociate"
              onClick={(e) => {
                e.stopPropagation()
                associateMutation.mutate({ id: row.id, port_id: '' })
              }}
            >
              <Unlink className="h-4 w-4 text-muted-foreground" />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            title="Release"
            onClick={(e) => { e.stopPropagation(); setDeleteTarget(row) }}
          >
            <Trash2 className="h-4 w-4 text-muted-foreground" />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Floating IPs"
        description="Externally routable IP addresses"
        action={
          <Button onClick={() => allocateMutation.mutate()} disabled={allocateMutation.isPending}>
            <Plus className="h-4 w-4" />
            {allocateMutation.isPending ? 'Allocating…' : 'Allocate IP'}
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={fips as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No floating IPs allocated."
      />
      <AssociateDialog
        fip={associateTarget}
        usedPortIds={new Set(fips.map((f) => f.port_id).filter(Boolean) as string[])}
        onOpenChange={(open) => !open && setAssociateTarget(null)}
        onSubmit={(portId) => associateTarget && associateMutation.mutate({ id: associateTarget.id, port_id: portId })}
        loading={associateMutation.isPending}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Release Floating IP"
        description={`Release floating IP "${deleteTarget?.floating_ip_address}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

function AssociateDialog({
  fip,
  usedPortIds,
  onOpenChange,
  onSubmit,
  loading,
}: {
  fip: FloatingIP | null
  usedPortIds: Set<string>
  onOpenChange: (open: boolean) => void
  onSubmit: (portId: string) => void
  loading: boolean
}) {
  const [portId, setPortId] = useState('')

  const { data: ports = [] } = useQuery({
    queryKey: ['ports'],
    queryFn: listPorts,
    enabled: !!fip,
  })
  const { data: instances = [] } = useQuery({
    queryKey: ['instances'],
    queryFn: listInstances,
    enabled: !!fip,
  })
  const { data: networks = [] } = useQuery({
    queryKey: ['networks'],
    queryFn: listNetworks,
    enabled: !!fip,
  })

  const networkName = (id: string) =>
    networks.find((n) => n.id === id)?.name ?? id.slice(0, 8) + '…'

  const portToInstance = new Map<string, string>()
  for (const inst of instances) {
    for (const pid of inst.port_ids ?? []) {
      portToInstance.set(pid, inst.name || inst.id.slice(0, 8))
    }
  }

  // Exclude system ports (router interfaces, floatingip claims) and already-used ports
  const availablePorts = ports.filter((p) =>
    p.fixed_ips?.length > 0 &&
    !usedPortIds.has(p.id) &&
    !p.device_id?.startsWith('router:') &&
    !p.device_id?.startsWith('floatingip:')
  )

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (portId) onSubmit(portId)
  }

  return (
    <Dialog open={!!fip} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Associate Floating IP</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground font-mono">{fip?.floating_ip_address}</p>
        <form onSubmit={handleSubmit} className="space-y-3">
          <p className="text-sm text-muted-foreground">Select an instance port to associate with:</p>
          {availablePorts.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4 text-center">No eligible ports available.</p>
          ) : (
            <div className="max-h-60 overflow-y-auto rounded-md border border-input divide-y divide-border">
              {availablePorts.map((p) => {
                const instance = portToInstance.get(p.id)
                const ip = p.fixed_ips[0]?.ip_address
                const net = networkName(p.network_id)
                const selected = portId === p.id
                return (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => setPortId(p.id)}
                    className={`w-full text-left px-4 py-3 transition-colors hover:bg-accent ${selected ? 'bg-accent' : ''}`}
                  >
                    <div className="font-medium text-sm">{instance ?? p.name ?? 'Unattached'}</div>
                    <div className="text-xs text-muted-foreground mt-0.5">{ip} · {net}</div>
                  </button>
                )
              })}
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={!portId || loading}>
              {loading ? 'Associating…' : 'Associate'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
