import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Globe } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { toast } from '@/hooks/use-toast'
import { listInstances, deleteInstance } from '@/api/compute'
import { listPorts, listFloatingIPs } from '@/api/network'
import type { Instance } from '@/types/compute'
import type { Port, FloatingIP } from '@/types/network'

export default function InstancesPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()

  const [deleteTarget, setDeleteTarget] = useState<Instance | null>(null)

  const { data: instances = [], isLoading, error } = useQuery({
    queryKey: ['instances'],
    queryFn: listInstances,
    refetchInterval: 10_000,
  })
  const { data: ports } = useQuery({
    queryKey: ['ports'],
    queryFn: listPorts,
    refetchInterval: 10_000,
  })
  const { data: floatingIPs } = useQuery({
    queryKey: ['floatingips'],
    queryFn: listFloatingIPs,
    refetchInterval: 10_000,
  })

  const deleteMutation = useMutation({
    mutationFn: deleteInstance,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['instances'] })
      setDeleteTarget(null)
      toast({ title: 'Instance deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Instance>[] = [
    { key: 'name', header: 'Name' },
    {
      key: 'status',
      header: 'Status',
      render: (row) => <StatusBadge status={row.status} />,
    },
    { key: 'flavor_id', header: 'Flavor ID' },
    {
      key: 'node_id',
      header: 'Node',
      render: (row) => <span className="text-muted-foreground">{row.node_id ?? '—'}</span>,
    },
    {
      key: 'ip_addresses',
      header: 'IP Addresses',
      render: (row) => {
        const instancePorts = (ports ?? []).filter(
          (p: Port) => p.device_id === row.id || (row.port_ids ?? []).includes(p.id)
        )
        const instancePortIds = new Set(instancePorts.map((p: Port) => p.id))
        const fixedIps = instancePorts.flatMap((p: Port) => p.fixed_ips?.map((f) => f.ip_address) ?? [])
        const fips = (floatingIPs ?? []).filter(
          (f: FloatingIP) => f.port_id && instancePortIds.has(f.port_id)
        )

        if (fixedIps.length === 0 && fips.length === 0) {
          return <span className="text-muted-foreground">—</span>
        }

        return (
          <div className="flex flex-col gap-0.5">
            {fixedIps.map((ip) => (
              <span key={ip} className="font-mono text-xs">{ip}</span>
            ))}
            {fips.map((f: FloatingIP) => (
              <span key={f.id} className="flex items-center gap-1 font-mono text-xs text-sky-400">
                <Globe className="h-3 w-3 shrink-0" />
                {f.floating_ip_address}
              </span>
            ))}
          </div>
        )
      },
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (row) => new Date(row.created_at).toLocaleString(),
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
        title="Instances"
        description="Manage your virtual machine instances"
        action={
          <Button onClick={() => navigate('/compute/instances/launch')}>
            <Plus className="h-4 w-4" />
            Launch Instance
          </Button>
        }
      />

      <ResourceTable
        columns={columns as unknown as Column<Record<string, unknown>>[]}
        data={instances as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        onRowClick={(row) => navigate(`/compute/instances/${(row as unknown as Instance).id}`)}
        emptyMessage="No instances yet. Launch one to get started."
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Instance"
        description={`Are you sure you want to delete "${deleteTarget?.name}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}
