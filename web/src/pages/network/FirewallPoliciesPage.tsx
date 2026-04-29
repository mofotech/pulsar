import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Pencil, ShieldAlert } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toast } from '@/hooks/use-toast'
import {
  listFirewallPolicies,
  createFirewallPolicy,
  updateFirewallPolicy,
  deleteFirewallPolicy,
} from '@/api/network'
import { listRouters } from '@/api/network'
import type { FirewallPolicy, FirewallRule } from '@/types/network'

const EMPTY_RULE: FirewallRule = {
  direction: 'ingress',
  action: 'allow',
  priority: 100,
}

export default function FirewallPoliciesPage() {
  const qc = useQueryClient()

  const { data: policies = [], isLoading } = useQuery({
    queryKey: ['firewall-policies'],
    queryFn: listFirewallPolicies,
  })

  const { data: routers = [] } = useQuery({
    queryKey: ['routers'],
    queryFn: listRouters,
  })

  const [policyDialog, setPolicyDialog] = useState(false)
  const [editingPolicy, setEditingPolicy] = useState<FirewallPolicy | null>(null)
  const [policyName, setPolicyName] = useState('')
  const [policyRouterID, setPolicyRouterID] = useState('')
  const [policyRules, setPolicyRules] = useState<FirewallRule[]>([])
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const createMutation = useMutation({
    mutationFn: () => createFirewallPolicy({ name: policyName, router_id: policyRouterID, rules: policyRules }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['firewall-policies'] })
      setPolicyDialog(false)
      toast({ title: 'Firewall policy created' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Create failed', description: err.message }),
  })

  const updateMutation = useMutation({
    mutationFn: () => updateFirewallPolicy(editingPolicy!.id, { name: policyName, rules: policyRules }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['firewall-policies'] })
      setPolicyDialog(false)
      toast({ title: 'Firewall policy updated' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Update failed', description: err.message }),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteFirewallPolicy(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['firewall-policies'] })
      toast({ title: 'Firewall policy deleted' })
    },
    onError: (err: Error) => toast({ variant: 'destructive', title: 'Delete failed', description: err.message }),
  })

  function openCreate() {
    setEditingPolicy(null)
    setPolicyName('')
    setPolicyRouterID('')
    setPolicyRules([])
    setPolicyDialog(true)
  }

  function openEdit(policy: FirewallPolicy) {
    setEditingPolicy(policy)
    setPolicyName(policy.name)
    setPolicyRouterID(policy.router_id)
    setPolicyRules(policy.rules ?? [])
    setPolicyDialog(true)
  }

  function addRule() {
    setPolicyRules(r => [...r, { ...EMPTY_RULE }])
  }

  function removeRule(idx: number) {
    setPolicyRules(r => r.filter((_, i) => i !== idx))
  }

  function updateRule(idx: number, patch: Partial<FirewallRule>) {
    setPolicyRules(r => r.map((rule, i) => i === idx ? { ...rule, ...patch } : rule))
  }

  const saving = createMutation.isPending || updateMutation.isPending

  return (
    <div>
      <PageHeader
        title="Firewall Policies"
        description="Per-router ingress/egress firewall ACLs applied to the gateway port"
        action={
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4" />
            New Policy
          </Button>
        }
      />

      {isLoading ? (
        <div className="space-y-3">
          {[1, 2].map(i => <div key={i} className="h-24 animate-pulse rounded bg-muted" />)}
        </div>
      ) : policies.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted-foreground">
          <ShieldAlert className="h-10 w-10" />
          <p>No firewall policies. Create one to restrict traffic on a router gateway.</p>
        </div>
      ) : (
        <div className="space-y-4">
          {policies.map(policy => (
            <Card key={policy.id}>
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <div>
                  <CardTitle className="text-base">{policy.name}</CardTitle>
                  <p className="text-xs text-muted-foreground font-mono mt-0.5">
                    Router: {policy.router_id}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button variant="outline" size="sm" onClick={() => openEdit(policy)}>
                    <Pencil className="h-3.5 w-3.5" />
                    Edit
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => setDeleteTarget(policy.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    Delete
                  </Button>
                </div>
              </CardHeader>
              <CardContent>
                {policy.rules && policy.rules.length > 0 ? (
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="text-muted-foreground border-b">
                        <th className="text-left pb-1 font-medium">Direction</th>
                        <th className="text-left pb-1 font-medium">Protocol</th>
                        <th className="text-left pb-1 font-medium">Ports</th>
                        <th className="text-left pb-1 font-medium">Src CIDR</th>
                        <th className="text-left pb-1 font-medium">Dst CIDR</th>
                        <th className="text-left pb-1 font-medium">Action</th>
                        <th className="text-left pb-1 font-medium">Priority</th>
                      </tr>
                    </thead>
                    <tbody>
                      {policy.rules.map((rule, i) => (
                        <tr key={i} className="border-b last:border-0">
                          <td className="py-1">{rule.direction}</td>
                          <td className="py-1">{rule.protocol || 'any'}</td>
                          <td className="py-1">
                            {rule.port_min ? (rule.port_min === rule.port_max ? rule.port_min : `${rule.port_min}–${rule.port_max}`) : 'any'}
                          </td>
                          <td className="py-1 font-mono">{rule.src_cidr || 'any'}</td>
                          <td className="py-1 font-mono">{rule.dst_cidr || 'any'}</td>
                          <td className="py-1">
                            <span className={rule.action === 'allow' ? 'text-green-600 font-medium' : 'text-red-600 font-medium'}>
                              {rule.action}
                            </span>
                          </td>
                          <td className="py-1">{rule.priority}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : (
                  <p className="text-xs text-muted-foreground">No rules — all traffic is dropped by default.</p>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Create / Edit dialog */}
      <Dialog open={policyDialog} onOpenChange={setPolicyDialog}>
        <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editingPolicy ? 'Edit Firewall Policy' : 'New Firewall Policy'}</DialogTitle>
          </DialogHeader>

          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label>Name</Label>
                <Input value={policyName} onChange={e => setPolicyName(e.target.value)} placeholder="e.g. web-tier-fw" />
              </div>
              <div className="space-y-1.5">
                <Label>Router</Label>
                <Select value={policyRouterID} onValueChange={setPolicyRouterID} disabled={!!editingPolicy}>
                  <SelectTrigger>
                    <SelectValue placeholder="Select router" />
                  </SelectTrigger>
                  <SelectContent>
                    {(routers as Array<{id: string; name: string}>).map(r => (
                      <SelectItem key={r.id} value={r.id}>{r.name || r.id}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div>
              <div className="flex items-center justify-between mb-2">
                <Label>Rules</Label>
                <Button variant="outline" size="sm" onClick={addRule}>
                  <Plus className="h-3.5 w-3.5" />
                  Add Rule
                </Button>
              </div>

              {policyRules.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4 text-center border rounded">
                  No rules — a default deny will be applied. Add rules to permit traffic.
                </p>
              ) : (
                <div className="space-y-2">
                  {policyRules.map((rule, idx) => (
                    <div key={idx} className="grid grid-cols-8 gap-2 items-end rounded border p-2">
                      <div className="space-y-1">
                        <Label className="text-xs">Direction</Label>
                        <Select value={rule.direction} onValueChange={v => updateRule(idx, { direction: v as 'ingress' | 'egress' })}>
                          <SelectTrigger className="h-8 text-xs"><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="ingress">ingress</SelectItem>
                            <SelectItem value="egress">egress</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Protocol</Label>
                        <Select
                          value={rule.protocol ?? '__any__'}
                          onValueChange={v => {
                            const proto = v === '__any__' ? undefined : v as FirewallRule['protocol']
                            const clearPorts = proto !== 'tcp' && proto !== 'udp'
                            updateRule(idx, {
                              protocol: proto,
                              ...(clearPorts && { port_min: undefined, port_max: undefined }),
                            })
                          }}
                        >
                          <SelectTrigger className="h-8 text-xs"><SelectValue placeholder="any" /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="__any__">any</SelectItem>
                            <SelectItem value="tcp">tcp</SelectItem>
                            <SelectItem value="udp">udp</SelectItem>
                            <SelectItem value="icmp">icmp</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Port Min</Label>
                        <Input className="h-8 text-xs" type="number" min={1} max={65535}
                          disabled={rule.protocol !== 'tcp' && rule.protocol !== 'udp'}
                          value={rule.port_min ?? ''} onChange={e => updateRule(idx, { port_min: e.target.value ? +e.target.value : undefined })} />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Port Max</Label>
                        <Input className="h-8 text-xs" type="number" min={1} max={65535}
                          disabled={rule.protocol !== 'tcp' && rule.protocol !== 'udp'}
                          value={rule.port_max ?? ''} onChange={e => updateRule(idx, { port_max: e.target.value ? +e.target.value : undefined })} />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Src CIDR</Label>
                        <Input className="h-8 text-xs font-mono" placeholder="any"
                          value={rule.src_cidr ?? ''} onChange={e => updateRule(idx, { src_cidr: e.target.value || undefined })} />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Dst CIDR</Label>
                        <Input className="h-8 text-xs font-mono" placeholder="any"
                          value={rule.dst_cidr ?? ''} onChange={e => updateRule(idx, { dst_cidr: e.target.value || undefined })} />
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">Action</Label>
                        <Select value={rule.action} onValueChange={v => updateRule(idx, { action: v as 'allow' | 'drop' })}>
                          <SelectTrigger className="h-8 text-xs"><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="allow">allow</SelectItem>
                            <SelectItem value="drop">drop</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                      <div className="flex items-end gap-1">
                        <div className="flex-1 space-y-1">
                          <Label className="text-xs">Priority</Label>
                          <Input className="h-8 text-xs" type="number" min={0} max={999}
                            value={rule.priority} onChange={e => updateRule(idx, { priority: +e.target.value })} />
                        </div>
                        <Button variant="ghost" size="sm" className="h-8 w-8 p-0 text-destructive" onClick={() => removeRule(idx)}>
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setPolicyDialog(false)}>Cancel</Button>
            <Button
              disabled={!policyName.trim() || (!editingPolicy && !policyRouterID) || saving}
              onClick={() => editingPolicy ? updateMutation.mutate() : createMutation.mutate()}
            >
              {editingPolicy ? 'Save' : 'Create'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!deleteTarget}
        title="Delete Firewall Policy"
        description="This will remove all ACLs from the router's gateway port. Traffic will no longer be filtered."
        onConfirm={() => { deleteMutation.mutate(deleteTarget!); setDeleteTarget(null) }}
        onOpenChange={(open) => { if (!open) setDeleteTarget(null) }}
      />
    </div>
  )
}
