import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, ShieldCheck } from 'lucide-react'
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
  listSecurityGroups,
  getSecurityGroup,
  createSecurityGroup,
  deleteSecurityGroup,
  addSecurityGroupRule,
  deleteSecurityGroupRule,
} from '@/api/network'
import type { SecurityGroup, SecurityGroupRule } from '@/types/network'

// ─── Direction / protocol badge helpers ───────────────────────────────────────

function DirectionBadge({ direction }: { direction: string }) {
  const base = 'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-semibold'
  return direction === 'ingress' ? (
    <span className={`${base} bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200`}>ingress</span>
  ) : (
    <span className={`${base} bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200`}>egress</span>
  )
}

function portRangeText(rule: SecurityGroupRule): string {
  if (rule.port_range_min == null && rule.port_range_max == null) return 'any'
  if (rule.port_range_min === rule.port_range_max) return String(rule.port_range_min)
  return `${rule.port_range_min ?? '*'}–${rule.port_range_max ?? '*'}`
}

// ─── Main page ─────────────────────────────────────────────────────────────────

export default function SecurityGroupsPage() {
  const qc = useQueryClient()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<SecurityGroup | null>(null)
  const [addRuleOpen, setAddRuleOpen] = useState(false)
  const [deleteRuleTarget, setDeleteRuleTarget] = useState<SecurityGroupRule | null>(null)

  // Group list
  const { data: groups = [], isLoading, error } = useQuery({
    queryKey: ['security-groups'],
    queryFn: listSecurityGroups,
  })

  // Selected group detail (includes rules)
  const { data: selectedGroup } = useQuery({
    queryKey: ['security-group', selectedId],
    queryFn: () => getSecurityGroup(selectedId!),
    enabled: !!selectedId,
  })

  // ── Mutations ──────────────────────────────────────────────────────────────

  const createMutation = useMutation({
    mutationFn: createSecurityGroup,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['security-groups'] })
      setCreateOpen(false)
      toast({ title: 'Security group created' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Create failed', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteSecurityGroup,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['security-groups'] })
      if (deleteTarget?.id === selectedId) setSelectedId(null)
      setDeleteTarget(null)
      toast({ title: 'Security group deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const addRuleMutation = useMutation({
    mutationFn: (req: Parameters<typeof addSecurityGroupRule>[1]) =>
      addSecurityGroupRule(selectedId!, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['security-group', selectedId] })
      qc.invalidateQueries({ queryKey: ['security-groups'] })
      setAddRuleOpen(false)
      toast({ title: 'Rule added' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Add rule failed', description: err.message })
    },
  })

  const deleteRuleMutation = useMutation({
    mutationFn: (ruleId: string) => deleteSecurityGroupRule(selectedId!, ruleId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['security-group', selectedId] })
      qc.invalidateQueries({ queryKey: ['security-groups'] })
      setDeleteRuleTarget(null)
      toast({ title: 'Rule deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete rule failed', description: err.message })
    },
  })

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div>
      <PageHeader
        title="Security Groups"
        description="Firewall rule groups applied to ports"
        action={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            Create Group
          </Button>
        }
      />

      <div className="grid gap-6 lg:grid-cols-3">
        {/* ── Group list ── */}
        <Card className="lg:col-span-1">
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground uppercase tracking-wide">
              Groups
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading && (
              <div className="p-4 space-y-2">
                {[...Array(3)].map((_, i) => (
                  <div key={i} className="h-10 animate-pulse rounded bg-muted" />
                ))}
              </div>
            )}
            {error && (
              <p className="p-4 text-sm text-destructive">{(error as Error).message}</p>
            )}
            {!isLoading && !error && groups.length === 0 && (
              <p className="p-4 text-sm text-muted-foreground">No security groups.</p>
            )}
            {groups.map((g) => (
              <button
                key={g.id}
                onClick={() => setSelectedId(g.id === selectedId ? null : g.id)}
                className={[
                  'w-full flex items-center justify-between px-4 py-3 text-left text-sm transition-colors',
                  'hover:bg-muted/50 border-b last:border-b-0',
                  g.id === selectedId ? 'bg-muted font-medium' : '',
                ].join(' ')}
              >
                <div className="flex items-center gap-2 min-w-0">
                  <ShieldCheck className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <span className="truncate">{g.name}</span>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-xs text-muted-foreground">
                    {g.rules?.length ?? 0} rule{(g.rules?.length ?? 0) !== 1 ? 's' : ''}
                  </span>
                  <button
                    className="p-1 rounded hover:bg-muted text-muted-foreground hover:text-destructive transition-colors"
                    onClick={(e) => { e.stopPropagation(); setDeleteTarget(g) }}
                    title="Delete group"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </button>
            ))}
          </CardContent>
        </Card>

        {/* ── Rules panel ── */}
        <Card className="lg:col-span-2">
          {!selectedId ? (
            <CardContent className="flex flex-col items-center justify-center h-64 gap-2 text-muted-foreground">
              <ShieldCheck className="h-10 w-10 opacity-30" />
              <p className="text-sm">Select a security group to view its rules</p>
            </CardContent>
          ) : (
            <>
              <CardHeader className="flex flex-row items-center justify-between pb-2">
                <CardTitle className="text-base">{selectedGroup?.name ?? '…'}</CardTitle>
                <Button size="sm" onClick={() => setAddRuleOpen(true)}>
                  <Plus className="h-4 w-4" />
                  Add Rule
                </Button>
              </CardHeader>
              <CardContent>
                {!selectedGroup ? (
                  <div className="space-y-2">
                    {[...Array(2)].map((_, i) => (
                      <div key={i} className="h-10 animate-pulse rounded bg-muted" />
                    ))}
                  </div>
                ) : selectedGroup.rules && selectedGroup.rules.length > 0 ? (
                  <div className="rounded-md border overflow-hidden">
                    <table className="w-full text-sm">
                      <thead className="bg-muted/50">
                        <tr>
                          <th className="px-3 py-2 text-left font-medium text-muted-foreground text-xs">Direction</th>
                          <th className="px-3 py-2 text-left font-medium text-muted-foreground text-xs">Protocol</th>
                          <th className="px-3 py-2 text-left font-medium text-muted-foreground text-xs">Ports</th>
                          <th className="px-3 py-2 text-left font-medium text-muted-foreground text-xs">Remote CIDR</th>
                          <th className="px-3 py-2 text-left font-medium text-muted-foreground text-xs">Ethertype</th>
                          <th className="px-3 py-2" />
                        </tr>
                      </thead>
                      <tbody className="divide-y">
                        {selectedGroup.rules.map((rule) => (
                          <tr key={rule.id} className="hover:bg-muted/30">
                            <td className="px-3 py-2">
                              <DirectionBadge direction={rule.direction} />
                            </td>
                            <td className="px-3 py-2 font-mono text-xs">{rule.protocol ?? 'any'}</td>
                            <td className="px-3 py-2 font-mono text-xs">{portRangeText(rule)}</td>
                            <td className="px-3 py-2 font-mono text-xs">{rule.remote_ip_prefix ?? '0.0.0.0/0'}</td>
                            <td className="px-3 py-2 text-xs">{rule.ethertype}</td>
                            <td className="px-3 py-2 text-right">
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-7 w-7"
                                onClick={() => setDeleteRuleTarget(rule)}
                              >
                                <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                              </Button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    No rules. All traffic is blocked by default.
                  </p>
                )}
              </CardContent>
            </>
          )}
        </Card>
      </div>

      {/* ── Dialogs ── */}
      <CreateSecurityGroupDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(name) => createMutation.mutate({ name })}
        loading={createMutation.isPending}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Security Group"
        description={`Delete security group "${deleteTarget?.name}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />

      {selectedId && (
        <AddRuleDialog
          open={addRuleOpen}
          onOpenChange={setAddRuleOpen}
          onSubmit={(req) => addRuleMutation.mutate(req)}
          loading={addRuleMutation.isPending}
        />
      )}

      <ConfirmDialog
        open={!!deleteRuleTarget}
        onOpenChange={(open) => !open && setDeleteRuleTarget(null)}
        title="Delete Rule"
        description="Delete this firewall rule? This cannot be undone."
        onConfirm={() => deleteRuleTarget && deleteRuleMutation.mutate(deleteRuleTarget.id)}
        loading={deleteRuleMutation.isPending}
        destructive
      />
    </div>
  )
}

// ─── Create Security Group dialog ─────────────────────────────────────────────

function CreateSecurityGroupDialog({
  open, onOpenChange, onSubmit, loading,
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
      <DialogContent className="sm:max-w-sm">
        <DialogHeader><DialogTitle>Create Security Group</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="sg-name">Name</Label>
            <Input
              id="sg-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-group"
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

// ─── Add Rule dialog ───────────────────────────────────────────────────────────

type AddRuleFormData = Parameters<typeof addSecurityGroupRule>[1]

function AddRuleDialog({
  open, onOpenChange, onSubmit, loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: AddRuleFormData) => void
  loading: boolean
}) {
  const [direction, setDirection] = useState('ingress')
  const [protocol, setProtocol] = useState('any')
  const [portMin, setPortMin] = useState('')
  const [portMax, setPortMax] = useState('')
  const [cidr, setCidr] = useState('')
  const [ethertype, setEthertype] = useState('IPv4')

  const showPorts = protocol === 'tcp' || protocol === 'udp'

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const req: AddRuleFormData = {
      direction,
      ethertype,
      ...(protocol !== 'any' && { protocol }),
      ...(showPorts && portMin && { port_range_min: Number(portMin) }),
      ...(showPorts && portMax && { port_range_max: Number(portMax) }),
      ...(cidr && { remote_ip_prefix: cidr }),
    }
    onSubmit(req)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>Add Rule</DialogTitle></DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Direction</Label>
              <Select value={direction} onValueChange={setDirection}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="ingress">Ingress</SelectItem>
                  <SelectItem value="egress">Egress</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>Protocol</Label>
              <Select value={protocol} onValueChange={setProtocol}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">Any</SelectItem>
                  <SelectItem value="tcp">TCP</SelectItem>
                  <SelectItem value="udp">UDP</SelectItem>
                  <SelectItem value="icmp">ICMP</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {showPorts && (
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label htmlFor="port-min">Port Min</Label>
                <Input
                  id="port-min"
                  type="number"
                  min={1}
                  max={65535}
                  value={portMin}
                  onChange={(e) => setPortMin(e.target.value)}
                  placeholder="1"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="port-max">Port Max</Label>
                <Input
                  id="port-max"
                  type="number"
                  min={1}
                  max={65535}
                  value={portMax}
                  onChange={(e) => setPortMax(e.target.value)}
                  placeholder="65535"
                />
              </div>
            </div>
          )}

          <div className="space-y-2">
            <Label htmlFor="cidr">Remote CIDR</Label>
            <Input
              id="cidr"
              value={cidr}
              onChange={(e) => setCidr(e.target.value)}
              placeholder="0.0.0.0/0"
            />
          </div>

          <div className="space-y-2">
            <Label>Ethertype</Label>
            <Select value={ethertype} onValueChange={setEthertype}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="IPv4">IPv4</SelectItem>
                <SelectItem value="IPv6">IPv6</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? 'Adding…' : 'Add Rule'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
