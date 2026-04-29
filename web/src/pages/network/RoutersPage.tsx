import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Globe, X } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
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
import {
  listRouters,
  createRouter,
  deleteRouter,
  setRouterGateway,
  clearRouterGateway,
  listNetworks,
  listSubnets,
  addRouterInterface,
  removeRouterInterface,
} from '@/api/network'
import type { Router, Network, Subnet } from '@/types/network'

export default function RoutersPage() {
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [gatewayTarget, setGatewayTarget] = useState<Router | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Router | null>(null)
  const [interfaceTarget, setInterfaceTarget] = useState<Router | null>(null)

  const { data: routers = [], isLoading, error } = useQuery({
    queryKey: ['routers'],
    queryFn: listRouters,
  })

  const { data: networks = [] } = useQuery({
    queryKey: ['networks'],
    queryFn: listNetworks,
  })

  const { data: subnets = [] } = useQuery({
    queryKey: ['subnets'],
    queryFn: listSubnets,
  })

  const externalNetworks = (networks as Network[]).filter((n) => n.external)

  const createMutation = useMutation({
    mutationFn: (name: string) => createRouter({ name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      setCreateOpen(false)
      toast({ title: 'Router created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const setGatewayMutation = useMutation({
    mutationFn: ({ id, networkId }: { id: string; networkId: string }) =>
      setRouterGateway(id, { external_network_id: networkId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      setGatewayTarget(null)
      toast({ title: 'Gateway set' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Set gateway failed', description: err.message })
    },
  })

  const clearGatewayMutation = useMutation({
    mutationFn: (id: string) => clearRouterGateway(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      toast({ title: 'Gateway cleared' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Clear gateway failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteRouter,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      setDeleteTarget(null)
      toast({ title: 'Router deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const addInterfaceMutation = useMutation({
    mutationFn: ({ id, subnetId }: { id: string; subnetId: string }) =>
      addRouterInterface(id, { subnet_id: subnetId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      setInterfaceTarget(null)
      toast({ title: 'Interface added' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Add interface failed', description: err.message })
    },
  })

  const removeInterfaceMutation = useMutation({
    mutationFn: ({ id, subnetId }: { id: string; subnetId: string }) =>
      removeRouterInterface(id, { subnet_id: subnetId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['routers'] })
      toast({ title: 'Interface removed' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Remove interface failed', description: err.message })
    },
  })

  const columns: Column<Router>[] = [
    { key: 'name', header: 'Name' },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    {
      key: 'external_network_id',
      header: 'Gateway Network',
      render: (r) => {
        if (!r.external_network_id) return <span className="text-muted-foreground">—</span>
        const net = externalNetworks.find((n) => n.id === r.external_network_id)
        return (
          <Badge variant="outline" className="gap-1 border-sky-500/40 text-sky-400">
            <Globe className="h-3 w-3" />
            {net?.name ?? r.external_network_id.slice(0, 8)}
          </Badge>
        )
      },
    },
    { key: 'external_ip', header: 'External IP', render: (r) => r.external_ip ?? '—' },
    {
      key: 'interface_subnets',
      header: 'Internal Interfaces',
      render: (r) => {
        const attached = r.interface_subnets ?? []
        if (attached.length === 0) return <span className="text-muted-foreground">—</span>
        return (
          <div className="flex flex-wrap gap-1">
            {attached.map((sid) => {
              const sub = (subnets as Subnet[]).find((s) => s.id === sid)
              return (
                <Badge
                  key={sid}
                  variant="outline"
                  className="gap-1 border-emerald-500/40 text-emerald-400 pr-0.5"
                >
                  {sub ? `${sub.name ?? sub.cidr}` : sid.slice(0, 8)}
                  <button
                    className="ml-1 rounded-sm opacity-70 hover:opacity-100 hover:text-destructive"
                    onClick={(e) => {
                      e.stopPropagation()
                      removeInterfaceMutation.mutate({ id: r.id, subnetId: sid })
                    }}
                    disabled={removeInterfaceMutation.isPending}
                    title="Remove interface"
                  >
                    <X className="h-3 w-3" />
                  </button>
                </Badge>
              )
            })}
          </div>
        )
      },
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (r) => new Date(r.created_at).toLocaleString(),
    },
    {
      key: 'actions',
      header: '',
      render: (row) => (
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-foreground"
            onClick={(e) => { e.stopPropagation(); setInterfaceTarget(row) }}
          >
            <Plus className="h-3 w-3" />
            Add interface
          </Button>
          {row.external_network_id ? (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-destructive"
              onClick={(e) => { e.stopPropagation(); clearGatewayMutation.mutate(row.id) }}
              disabled={clearGatewayMutation.isPending}
            >
              <X className="h-3 w-3" />
              Clear gateway
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-foreground"
              onClick={(e) => { e.stopPropagation(); setGatewayTarget(row) }}
            >
              <Globe className="h-3 w-3" />
              Set gateway
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
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
        title="Routers"
        description="Virtual routers and NAT gateways"
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            Create Router
          </Button>
        }
      />
      <ResourceTable
        columns={columns}
        data={routers as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No routers."
      />

      <CreateRouterDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(name) => createMutation.mutate(name)}
        loading={createMutation.isPending}
      />

      {gatewayTarget && (
        <SetGatewayDialog
          router={gatewayTarget}
          externalNetworks={externalNetworks}
          open={!!gatewayTarget}
          onOpenChange={(open) => !open && setGatewayTarget(null)}
          onSubmit={(networkId) =>
            setGatewayMutation.mutate({ id: gatewayTarget.id, networkId })
          }
          loading={setGatewayMutation.isPending}
        />
      )}

      {interfaceTarget && (
        <AddInterfaceDialog
          router={interfaceTarget}
          subnets={subnets as Subnet[]}
          open={!!interfaceTarget}
          onOpenChange={(open) => !open && setInterfaceTarget(null)}
          onSubmit={(subnetId) =>
            addInterfaceMutation.mutate({ id: interfaceTarget.id, subnetId })
          }
          loading={addInterfaceMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Router"
        description={`Delete router "${deleteTarget?.name}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}

function CreateRouterDialog({
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
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Router</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="router-name">Name</Label>
            <Input
              id="router-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-router"
              required
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SetGatewayDialog({
  router,
  externalNetworks,
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  router: Router
  externalNetworks: Network[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (networkId: string) => void
  loading: boolean
}) {
  const [networkId, setNetworkId] = useState(externalNetworks[0]?.id ?? '')

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (networkId) onSubmit(networkId)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Set Gateway — {router.name}</DialogTitle>
        </DialogHeader>
        {externalNetworks.length === 0 ? (
          <div className="py-4 text-sm text-muted-foreground">
            No external networks found. Create a network with the <strong>External</strong> flag enabled first.
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label>External Network</Label>
              <Select value={networkId} onValueChange={setNetworkId}>
                <SelectTrigger>
                  <SelectValue placeholder="Select external network…" />
                </SelectTrigger>
                <SelectContent>
                  {externalNetworks.map((n) => (
                    <SelectItem key={n.id} value={n.id}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                The router will NAT outbound traffic through this network.
              </p>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
                Cancel
              </Button>
              <Button type="submit" disabled={loading || !networkId}>
                {loading ? 'Setting…' : 'Set Gateway'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function AddInterfaceDialog({
  router,
  subnets,
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  router: Router
  subnets: Subnet[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (subnetId: string) => void
  loading: boolean
}) {
  const attached = router.interface_subnets ?? []
  const available = subnets.filter((s) => !attached.includes(s.id))
  const [subnetId, setSubnetId] = useState(available[0]?.id ?? '')

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (subnetId) onSubmit(subnetId)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add Interface — {router.name}</DialogTitle>
        </DialogHeader>
        {available.length === 0 ? (
          <div className="py-4 text-sm text-muted-foreground">
            No available subnets. All subnets are already attached, or no subnets exist.
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label>Subnet</Label>
              <Select value={subnetId} onValueChange={setSubnetId}>
                <SelectTrigger>
                  <SelectValue placeholder="Select subnet…" />
                </SelectTrigger>
                <SelectContent>
                  {available.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.name ? `${s.name} (${s.cidr})` : s.cidr}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                The router will be connected to this subnet and assigned its gateway IP.
              </p>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
                Cancel
              </Button>
              <Button type="submit" disabled={loading || !subnetId}>
                {loading ? 'Adding…' : 'Add Interface'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
