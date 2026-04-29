import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Globe, Pencil, ChevronDown, ChevronRight, GitBranch } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
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
import { listNetworks, createNetwork, updateNetwork, deleteNetwork,
  listSubnets, createSubnet, deleteSubnet,
} from '@/api/network'
import type { Network, Subnet, AllocationPool } from '@/types/network'

export default function NetworksPage() {
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Network | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Network | null>(null)
  const [expandedNetworks, setExpandedNetworks] = useState<Set<string>>(new Set())
  const [addSubnetFor, setAddSubnetFor] = useState<Network | null>(null)
  const [deleteSubnetTarget, setDeleteSubnetTarget] = useState<Subnet | null>(null)

  const { data: networks = [], isLoading, error } = useQuery({
    queryKey: ['networks'],
    queryFn: listNetworks,
  })

  const { data: allSubnets = [] } = useQuery({
    queryKey: ['subnets'],
    queryFn: listSubnets,
  })

  const subnetsByNetwork = (allSubnets as Subnet[]).reduce<Record<string, Subnet[]>>((acc, s) => {
    acc[s.network_id] = [...(acc[s.network_id] ?? []), s]
    return acc
  }, {})

  function toggleExpand(id: string) {
    setExpandedNetworks((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const createMutation = useMutation({
    mutationFn: createNetwork,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['networks'] })
      setCreateOpen(false)
      toast({ title: 'Network created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, req }: { id: string; req: { name?: string; external?: boolean } }) =>
      updateNetwork(id, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['networks'] })
      setEditTarget(null)
      toast({ title: 'Network updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteNetwork,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['networks'] })
      setDeleteTarget(null)
      toast({ title: 'Network deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const createSubnetMutation = useMutation({
    mutationFn: createSubnet,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['subnets'] })
      if (addSubnetFor) {
        setExpandedNetworks((prev) => new Set([...prev, addSubnetFor.id]))
      }
      setAddSubnetFor(null)
      toast({ title: 'Subnet created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const deleteSubnetMutation = useMutation({
    mutationFn: deleteSubnet,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['subnets'] })
      setDeleteSubnetTarget(null)
      toast({ title: 'Subnet deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  return (
    <div>
      <PageHeader
        title="Networks"
        description="Virtual networks and their subnets"
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            Create Network
          </Button>
        }
      />

      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {(error as Error).message}
        </div>
      )}

      {isLoading ? (
        <div className="py-12 text-center text-sm text-muted-foreground">Loading…</div>
      ) : (networks as Network[]).length === 0 ? (
        <div className="py-12 text-center text-sm text-muted-foreground">No networks.</div>
      ) : (
        <div className="rounded-lg border border-border/50 overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border/50 bg-muted/30">
                <th className="w-8 px-3 py-2.5" />
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Name</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Type</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">VNI / VLAN</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Scope</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">Status</th>
                <th className="px-4 py-2.5" />
              </tr>
            </thead>
            <tbody>
              {(networks as Network[]).map((network) => {
                const expanded = expandedNetworks.has(network.id)
                const subnets = subnetsByNetwork[network.id] ?? []
                return (
                  <>
                    {/* Network row */}
                    <tr
                      key={network.id}
                      className="border-b border-border/30 hover:bg-white/[0.02] cursor-pointer"
                      onClick={() => toggleExpand(network.id)}
                    >
                      <td className="px-3 py-3 text-muted-foreground">
                        {expanded
                          ? <ChevronDown className="h-3.5 w-3.5" />
                          : <ChevronRight className="h-3.5 w-3.5" />}
                      </td>
                      <td className="px-4 py-3 font-medium">{network.name}</td>
                      <td className="px-4 py-3 text-muted-foreground capitalize">{network.type}</td>
                      <td className="px-4 py-3 text-muted-foreground">
                        {network.vni ? `VNI ${network.vni}` : network.vlan_id ? `VLAN ${network.vlan_id}` : '—'}
                      </td>
                      <td className="px-4 py-3">
                        {network.external
                          ? (
                            <Badge variant="outline" className="gap-1 border-sky-500/40 text-sky-400">
                              <Globe className="h-3 w-3" />External
                            </Badge>
                          )
                          : <span className="text-muted-foreground text-xs">Internal</span>}
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge status={network.status} />
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost" size="icon"
                            onClick={(e) => { e.stopPropagation(); setEditTarget(network) }}
                          >
                            <Pencil className="h-4 w-4 text-muted-foreground" />
                          </Button>
                          <Button
                            variant="ghost" size="icon"
                            onClick={(e) => { e.stopPropagation(); setDeleteTarget(network) }}
                          >
                            <Trash2 className="h-4 w-4 text-muted-foreground" />
                          </Button>
                        </div>
                      </td>
                    </tr>

                    {/* Subnet rows */}
                    {expanded && (
                      <>
                        {subnets.map((subnet) => (
                          <tr key={subnet.id} className="border-b border-border/20 bg-muted/10">
                            <td className="px-3 py-2" />
                            <td className="px-4 py-2.5 pl-10" colSpan={2}>
                              <div className="flex items-center gap-2 text-muted-foreground">
                                <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted-foreground/50" />
                                <span className="font-mono text-xs">{subnet.cidr}</span>
                                {subnet.name && (
                                  <span className="text-xs text-muted-foreground/70">— {subnet.name}</span>
                                )}
                              </div>
                            </td>
                            <td className="px-4 py-2.5 text-xs text-muted-foreground">
                              GW: {subnet.gateway_ip ?? '—'}
                            </td>
                            <td className="px-4 py-2.5 text-xs text-muted-foreground" colSpan={2}>
                              {subnet.allocation_pool
                                ? <span className="font-mono">{subnet.allocation_pool.start} – {subnet.allocation_pool.end}</span>
                                : <span className="text-muted-foreground/40">full range</span>}
                            </td>
                            <td className="px-4 py-2.5">
                              <div className="flex justify-end">
                                <Button
                                  variant="ghost" size="icon"
                                  onClick={(e) => { e.stopPropagation(); setDeleteSubnetTarget(subnet) }}
                                >
                                  <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                                </Button>
                              </div>
                            </td>
                          </tr>
                        ))}
                        {/* Add subnet row */}
                        <tr className="border-b border-border/20 bg-muted/5">
                          <td className="px-3 py-2" />
                          <td className="px-4 py-2 pl-10" colSpan={6}>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7 gap-1.5 text-xs text-muted-foreground hover:text-foreground"
                              onClick={(e) => { e.stopPropagation(); setAddSubnetFor(network) }}
                            >
                              <Plus className="h-3 w-3" />
                              Add Subnet
                            </Button>
                          </td>
                        </tr>
                      </>
                    )}
                  </>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      <CreateNetworkDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />
      {editTarget && (
        <EditNetworkDialog
          network={editTarget}
          open={!!editTarget}
          onOpenChange={(open) => !open && setEditTarget(null)}
          onSubmit={(req) => updateMutation.mutate({ id: editTarget.id, req })}
          loading={updateMutation.isPending}
        />
      )}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Network"
        description={`Delete network "${deleteTarget?.name}"?`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
      {addSubnetFor && (
        <AddSubnetDialog
          network={addSubnetFor}
          open={!!addSubnetFor}
          onOpenChange={(open) => !open && setAddSubnetFor(null)}
          onSubmit={(data) => createSubnetMutation.mutate(data)}
          loading={createSubnetMutation.isPending}
        />
      )}
      <ConfirmDialog
        open={!!deleteSubnetTarget}
        onOpenChange={(open) => !open && setDeleteSubnetTarget(null)}
        title="Delete Subnet"
        description={`Delete subnet "${deleteSubnetTarget?.name ?? deleteSubnetTarget?.cidr}"?`}
        onConfirm={() => deleteSubnetTarget && deleteSubnetMutation.mutate(deleteSubnetTarget.id)}
        loading={deleteSubnetMutation.isPending}
        destructive
      />
    </div>
  )
}

function CreateNetworkDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: Partial<Network>) => void
  loading: boolean
}) {
  const [name, setName] = useState('')
  const [type, setType] = useState('vxlan')
  const [vlanId, setVlanId] = useState('')
  const [external, setExternal] = useState(false)

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const payload: Partial<Network> = { name, type, external }
    if (type === 'vlan' && vlanId) payload.vlan_id = parseInt(vlanId)
    onSubmit(payload)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Network</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="net-name">Name</Label>
            <Input id="net-name" value={name} onChange={(e) => setName(e.target.value)} required />
          </div>
          <div className="space-y-2">
            <Label>Type</Label>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="vxlan">VXLAN</SelectItem>
                <SelectItem value="vlan">VLAN</SelectItem>
                <SelectItem value="flat">Flat</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {type === 'vlan' && (
            <div className="space-y-2">
              <Label htmlFor="net-vlan">VLAN ID</Label>
              <Input
                id="net-vlan"
                type="number"
                min="1"
                max="4094"
                value={vlanId}
                onChange={(e) => setVlanId(e.target.value)}
              />
            </div>
          )}
          <div className="flex items-center gap-2">
            <input
              id="net-external"
              type="checkbox"
              checked={external}
              onChange={(e) => setExternal(e.target.checked)}
              className="h-4 w-4 rounded border-input"
            />
            <Label htmlFor="net-external">External</Label>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>Cancel</Button>
            <Button type="submit" disabled={loading}>{loading ? 'Creating...' : 'Create'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function EditNetworkDialog({
  network,
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  network: Network
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { name?: string; external?: boolean }) => void
  loading: boolean
}) {
  const [name, setName] = useState(network.name)
  const [external, setExternal] = useState(network.external)

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({ name, external })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Edit Network — {network.name}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="edit-net-name">Name</Label>
            <Input
              id="edit-net-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <div className="rounded-lg border border-border/50 bg-muted/20 p-3 space-y-1">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium">External network</p>
                <p className="text-xs text-muted-foreground">
                  External networks are shared across all projects and can be used as NAT gateway uplinks.
                </p>
              </div>
              <input
                id="edit-net-external"
                type="checkbox"
                checked={external}
                onChange={(e) => setExternal(e.target.checked)}
                className="h-4 w-4 rounded border-input"
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2 text-xs text-muted-foreground">
            <div><span className="font-medium text-foreground/60">Type</span><br />{network.type.toUpperCase()}</div>
            {network.vni ? <div><span className="font-medium text-foreground/60">VNI</span><br />{network.vni}</div> : null}
            {network.vlan_id ? <div><span className="font-medium text-foreground/60">VLAN ID</span><br />{network.vlan_id}</div> : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function AddSubnetDialog({
  network,
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  network: Network
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: Partial<Subnet>) => void
  loading: boolean
}) {
  const [form, setForm] = useState({ name: '', cidr: '', gateway_ip: '', pool_start: '', pool_end: '' })
  const set = (k: keyof typeof form, v: string) => setForm((p) => ({ ...p, [k]: v }))

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const pool: AllocationPool | undefined =
      form.pool_start && form.pool_end
        ? { start: form.pool_start, end: form.pool_end }
        : undefined
    onSubmit({
      name: form.name || undefined,
      network_id: network.id,
      cidr: form.cidr,
      gateway_ip: form.gateway_ip || undefined,
      allocation_pool: pool,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add Subnet — {network.name}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label>Name <span className="text-muted-foreground">(optional)</span></Label>
            <Input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="my-subnet" />
          </div>
          <div className="space-y-2">
            <Label>CIDR</Label>
            <Input value={form.cidr} onChange={(e) => set('cidr', e.target.value)} placeholder="10.0.0.0/24" required />
          </div>
          <div className="space-y-2">
            <Label>Gateway IP <span className="text-muted-foreground">(optional, auto-detected)</span></Label>
            <Input value={form.gateway_ip} onChange={(e) => set('gateway_ip', e.target.value)} placeholder="10.0.0.1" />
          </div>
          <div className="rounded-lg border border-border/50 bg-muted/20 p-3 space-y-3">
            <div>
              <p className="text-sm font-medium">Allocation Pool <span className="text-muted-foreground font-normal">(optional)</span></p>
              <p className="text-xs text-muted-foreground mt-0.5">
                Restrict IP allocation to this range. Leave empty to use the full subnet.
              </p>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1.5">
                <Label className="text-xs">Start IP</Label>
                <Input
                  value={form.pool_start}
                  onChange={(e) => set('pool_start', e.target.value)}
                  placeholder="10.0.0.10"
                  className="h-8 text-sm font-mono"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">End IP</Label>
                <Input
                  value={form.pool_end}
                  onChange={(e) => set('pool_end', e.target.value)}
                  placeholder="10.0.0.200"
                  className="h-8 text-sm font-mono"
                />
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>Cancel</Button>
            <Button type="submit" disabled={loading || !form.cidr}>
              {loading ? 'Creating…' : 'Add Subnet'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
