import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Pencil, ShieldCheck, Trash2 } from 'lucide-react'
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
import { listPorts, deletePort, listSecurityGroups, updatePortSecurityGroups } from '@/api/network'
import type { Port, SecurityGroup } from '@/types/network'

export default function PortsPage() {
  const qc = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<Port | null>(null)
  const [editSGPort, setEditSGPort] = useState<Port | null>(null)

  const { data: ports = [], isLoading, error } = useQuery({
    queryKey: ['ports'],
    queryFn: listPorts,
  })

  const { data: securityGroups = [] } = useQuery({
    queryKey: ['security-groups'],
    queryFn: listSecurityGroups,
  })

  const deleteMutation = useMutation({
    mutationFn: deletePort,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ports'] })
      setDeleteTarget(null)
      toast({ title: 'Port deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const updateSGsMutation = useMutation({
    mutationFn: ({ portId, sgIds }: { portId: string; sgIds: string[] }) =>
      updatePortSecurityGroups(portId, sgIds),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ports'] })
      setEditSGPort(null)
      toast({ title: 'Security groups updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const sgById = new Map<string, SecurityGroup>(securityGroups.map((sg) => [sg.id, sg]))

  const columns: Column<Port>[] = [
    { key: 'name', header: 'Name', render: (r) => r.name ?? '—' },
    { key: 'network_id', header: 'Network ID' },
    { key: 'mac_address', header: 'MAC Address' },
    {
      key: 'fixed_ips',
      header: 'Fixed IPs',
      render: (r) =>
        r.fixed_ips.length > 0
          ? r.fixed_ips.map((ip) => ip.ip_address).join(', ')
          : '—',
    },
    {
      key: 'security_group_ids',
      header: 'Security Groups',
      render: (r) => {
        const ids = r.security_group_ids ?? []
        if (ids.length === 0) return <span className="text-muted-foreground text-xs">none</span>
        return (
          <div className="flex flex-wrap gap-1">
            {ids.map((sgId) => {
              const sg = sgById.get(sgId)
              return (
                <span
                  key={sgId}
                  className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs"
                  title={sgId}
                >
                  <ShieldCheck className="h-3 w-3 text-muted-foreground" />
                  {sg?.name ?? sgId.slice(0, 8)}
                </span>
              )
            })}
          </div>
        )
      },
    },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    { key: 'device_id', header: 'Device ID', render: (r) => r.device_id ?? '—' },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            title="Edit security groups"
            onClick={(e) => { e.stopPropagation(); setEditSGPort(row) }}
          >
            <Pencil className="h-4 w-4 text-muted-foreground" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            title="Delete port"
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
      <PageHeader title="Ports" description="Network ports" />
      <ResourceTable
        columns={columns}
        data={ports as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No ports."
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Port"
        description={`Delete port "${deleteTarget?.name ?? deleteTarget?.mac_address}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />

      {editSGPort && (
        <EditPortSGsDialog
          open={!!editSGPort}
          onOpenChange={(open) => !open && setEditSGPort(null)}
          port={editSGPort}
          allSecurityGroups={securityGroups}
          onSave={(portId, sgIds) => updateSGsMutation.mutate({ portId, sgIds })}
          saving={updateSGsMutation.isPending}
        />
      )}
    </div>
  )
}

function EditPortSGsDialog({
  open,
  onOpenChange,
  port,
  allSecurityGroups,
  onSave,
  saving,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  port: Port
  allSecurityGroups: SecurityGroup[]
  onSave: (portId: string, sgIds: string[]) => void
  saving: boolean
}) {
  const [selected, setSelected] = useState<Set<string>>(new Set(port.security_group_ids ?? []))

  useEffect(() => {
    if (open) setSelected(new Set(port.security_group_ids ?? []))
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const portLabel = port.name || port.mac_address

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Security Groups — {portLabel}</DialogTitle>
        </DialogHeader>
        <div className="space-y-2 max-h-72 overflow-y-auto py-1">
          {allSecurityGroups.length === 0 ? (
            <p className="text-sm text-muted-foreground py-4 text-center">No security groups available.</p>
          ) : (
            allSecurityGroups.map((sg) => {
              const checked = selected.has(sg.id)
              return (
                <button
                  key={sg.id}
                  type="button"
                  onClick={() => toggle(sg.id)}
                  className={[
                    'w-full flex items-center gap-3 rounded-md border px-3 py-2 text-sm text-left transition-colors',
                    checked
                      ? 'border-primary bg-primary/5'
                      : 'border-border hover:bg-muted/50',
                  ].join(' ')}
                >
                  <div className={[
                    'h-4 w-4 rounded border flex items-center justify-center shrink-0',
                    checked ? 'border-primary bg-primary' : 'border-muted-foreground',
                  ].join(' ')}>
                    {checked && (
                      <svg viewBox="0 0 10 8" fill="none" className="h-2.5 w-2.5 text-primary-foreground">
                        <path d="M1 4l3 3 5-6" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                      </svg>
                    )}
                  </div>
                  <div className="min-w-0">
                    <span className="font-medium">{sg.name}</span>
                    {sg.description && (
                      <span className="ml-2 text-muted-foreground text-xs truncate">{sg.description}</span>
                    )}
                    <span className="ml-2 text-muted-foreground text-xs">
                      {sg.rules?.length ?? 0} rule{(sg.rules?.length ?? 0) !== 1 ? 's' : ''}
                    </span>
                  </div>
                </button>
              )
            })
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => onSave(port.id, [...selected])} disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
