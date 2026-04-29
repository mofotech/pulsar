import { useState, useEffect } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Play, Square, RotateCcw, Zap, Monitor, ShieldCheck, Pencil, Globe, HardDrive, Plug, Unplug, Network as NetworkIcon, Plus, Trash2, Cpu } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PageHeader } from '@/components/common/PageHeader'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { ConsoleModal } from '@/components/compute/ConsoleModal'
import { toast } from '@/hooks/use-toast'
import { getInstance, instanceAction, resetInstance, attachInterface, detachInterface, setMetadataKey, deleteMetadataKey, resizeInstance, listFlavors } from '@/api/compute'
import { listNetworks, listPorts, listSecurityGroups, updatePortSecurityGroups, listFloatingIPs } from '@/api/network'
import type { Network } from '@/types/network'
import { listVolumes, volumeAction } from '@/api/storage'
import type { InstanceAction, Flavor } from '@/types/compute'
import type { Port, SecurityGroup, FloatingIP } from '@/types/network'
import type { Volume, VolumeActionRequest } from '@/types/storage'

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [consoleOpen, setConsoleOpen] = useState(false)
  const [metadataDialogOpen, setMetadataDialogOpen] = useState(false)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [metaKey, setMetaKey] = useState('')
  const [metaValue, setMetaValue] = useState('')
  const [resizeDialogOpen, setResizeDialogOpen] = useState(false)
  const [selectedFlavorId, setSelectedFlavorId] = useState('')

  const { data: instance, isLoading, error } = useQuery({
    queryKey: ['instance', id],
    queryFn: () => getInstance(id!),
    enabled: !!id,
    refetchInterval: 10_000,
  })

  const { data: allPorts } = useQuery({
    queryKey: ['ports'],
    queryFn: listPorts,
    refetchInterval: 10_000,
  })

  const { data: allSecurityGroups = [] } = useQuery({
    queryKey: ['security-groups'],
    queryFn: listSecurityGroups,
  })

  const { data: allFloatingIPs = [] } = useQuery({
    queryKey: ['floating-ips'],
    queryFn: listFloatingIPs,
    refetchInterval: 10_000,
  })

  const { data: allVolumes = [] } = useQuery({
    queryKey: ['volumes'],
    queryFn: listVolumes,
    refetchInterval: 10_000,
  })

  const { data: allNetworks = [] } = useQuery({
    queryKey: ['networks'],
    queryFn: listNetworks,
  })

  const { data: allFlavors = [] } = useQuery({
    queryKey: ['flavors'],
    queryFn: listFlavors,
  })

  const resizeMutation = useMutation({
    mutationFn: (flavorId: string) => resizeInstance(id!, { flavor_id: flavorId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['instance', id] })
      qc.invalidateQueries({ queryKey: ['instances'] })
      setResizeDialogOpen(false)
      toast({ title: 'Resize dispatched', description: 'The instance is being resized.' })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Resize failed', description: err.message }),
  })

  const attachInterfaceMutation = useMutation({
    mutationFn: (req: { network_id?: string; port_id?: string }) => attachInterface(id!, req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ports'] })
      qc.invalidateQueries({ queryKey: ['instance', id] })
      toast({ title: 'Interface attached' })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Attach failed', description: err.message }),
  })

  const detachInterfaceMutation = useMutation({
    mutationFn: ({ portId }: { portId: string }) => detachInterface(id!, portId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ports'] })
      qc.invalidateQueries({ queryKey: ['instance', id] })
      qc.invalidateQueries({ queryKey: ['floating-ips'] })
      toast({ title: 'Interface detached' })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Detach failed', description: err.message }),
  })

  const volumeActionMutation = useMutation({
    mutationFn: ({ volumeId, req }: { volumeId: string; req: VolumeActionRequest }) =>
      volumeAction(volumeId, req),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['volumes'] })
      toast({ title: `Volume ${vars.req.action} succeeded` })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Volume action failed', description: err.message }),
  })

  const instancePorts: Port[] = (allPorts ?? []).filter(
    (p: Port) => p.device_id === id || (instance?.port_ids ?? []).includes(p.id)
  )

  const floatingIPsByPort = new Map<string, FloatingIP>()
  for (const fip of allFloatingIPs as FloatingIP[]) {
    if (fip.port_id) floatingIPsByPort.set(fip.port_id, fip)
  }

  const actionMutation = useMutation({
    mutationFn: (action: InstanceAction) => instanceAction(id!, action),
    onSuccess: (_, action) => {
      qc.invalidateQueries({ queryKey: ['instance', id] })
      qc.invalidateQueries({ queryKey: ['instances'] })
      toast({ title: `Action "${action}" triggered` })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Action failed', description: err.message })
    },
  })

  const resetMutation = useMutation({
    mutationFn: () => resetInstance(id!),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['instance', id] })
      qc.invalidateQueries({ queryKey: ['instances'] })
      toast({ title: 'Instance reset to stopped' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Reset failed', description: err.message })
    },
  })

  const updateSGsMutation = useMutation({
    mutationFn: ({ portId, sgIds }: { portId: string; sgIds: string[] }) =>
      updatePortSecurityGroups(portId, sgIds),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ports'] })
      toast({ title: 'Security groups updated' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Update failed', description: err.message })
    },
  })

  const setMetadataKeyMutation = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) => setMetadataKey(id!, key, value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['instance', id] })
      setMetadataDialogOpen(false)
      toast({ title: editingKey ? 'Metadata updated' : 'Metadata added' })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Save failed', description: err.message }),
  })

  const deleteMetadataKeyMutation = useMutation({
    mutationFn: (key: string) => deleteMetadataKey(id!, key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['instance', id] })
      toast({ title: 'Metadata key deleted' })
    },
    onError: (err: Error) =>
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message }),
  })

  function openAddMetadata() {
    setEditingKey(null)
    setMetaKey('')
    setMetaValue('')
    setMetadataDialogOpen(true)
  }

  function openEditMetadata(key: string, value: string) {
    setEditingKey(key)
    setMetaKey(key)
    setMetaValue(value)
    setMetadataDialogOpen(true)
  }

  if (isLoading) {
    return (
      <div>
        <Button variant="ghost" size="sm" onClick={() => navigate(-1)} className="mb-4">
          <ArrowLeft className="h-4 w-4" />
          Back
        </Button>
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
      </div>
    )
  }

  if (error || !instance) {
    return (
      <div>
        <Button variant="ghost" size="sm" onClick={() => navigate(-1)} className="mb-4">
          <ArrowLeft className="h-4 w-4" />
          Back
        </Button>
        <p className="text-destructive">{error?.message ?? 'Instance not found'}</p>
      </div>
    )
  }

  const status = instance.status
  const canStart = ['stopped'].includes(status)
  const canStop = ['active'].includes(status)
  const canReboot = ['active'].includes(status)
  const canConsole = ['active'].includes(status)
  const canReset = status === 'error'
  const isUnknown = status === 'unknown'

  return (
    <div>
      <Button variant="ghost" size="sm" onClick={() => navigate(-1)} className="mb-4">
        <ArrowLeft className="h-4 w-4" />
        Back
      </Button>

      <PageHeader
        title={instance.name}
        description={`ID: ${instance.id}`}
        action={
          <div className="flex items-center gap-2">
            <StatusBadge status={instance.status} />
            {canStart && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => actionMutation.mutate('start')}
                disabled={actionMutation.isPending}
              >
                <Play className="h-4 w-4" />
                Start
              </Button>
            )}
            {canStop && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => actionMutation.mutate('stop')}
                disabled={actionMutation.isPending}
              >
                <Square className="h-4 w-4" />
                Stop
              </Button>
            )}
            {canReboot && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => actionMutation.mutate('reboot')}
                  disabled={actionMutation.isPending}
                >
                  <RotateCcw className="h-4 w-4" />
                  Reboot
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => actionMutation.mutate('hard-reboot')}
                  disabled={actionMutation.isPending}
                >
                  <Zap className="h-4 w-4" />
                  Hard Reboot
                </Button>
              </>
            )}
            {canConsole && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => setConsoleOpen(true)}
              >
                <Monitor className="h-4 w-4" />
                Console
              </Button>
            )}
            {(canStart || canStop) && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setSelectedFlavorId(instance.flavor_id)
                  setResizeDialogOpen(true)
                }}
              >
                <Cpu className="h-4 w-4" />
                Resize
              </Button>
            )}
            {canReset && (
              <Button
                size="sm"
                variant="outline"
                className="border-destructive/50 text-destructive hover:bg-destructive/10"
                onClick={() => resetMutation.mutate()}
                disabled={resetMutation.isPending}
              >
                <RotateCcw className="h-4 w-4" />
                {resetMutation.isPending ? 'Resetting…' : 'Reset to Stopped'}
              </Button>
            )}
          </div>
        }
      />

      {isUnknown && (
        <div className="mb-4 flex items-start gap-3 rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-4 py-3 text-sm text-yellow-400">
          <span className="mt-0.5 shrink-0">⚠</span>
          <span>
            The compute node hosting this instance is currently unreachable. The VM may still be running.
            State will recover automatically when the node reconnects.
          </span>
        </div>
      )}

      <ConsoleModal
        instanceId={instance.id}
        instanceName={instance.name}
        open={consoleOpen}
        onClose={() => setConsoleOpen(false)}
      />

      <div className="grid gap-6 lg:grid-cols-2">
        {/* Details */}
        <Card>
          <CardHeader>
            <CardTitle>Details</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-3 text-sm">
              <DetailRow label="Flavor ID" value={instance.flavor_id} />
              <DetailRow label="Image ID" value={instance.image_id} />
              <DetailRow label="Hypervisor" value={instance.hypervisor_type} />
              <DetailRow label="Node ID" value={instance.node_id ?? '—'} />
              <DetailRow label="Project ID" value={instance.project_id} />
              <DetailRow label="Created" value={new Date(instance.created_at).toLocaleString()} />
              <DetailRow label="Updated" value={new Date(instance.updated_at).toLocaleString()} />
            </dl>
          </CardContent>
        </Card>

        {/* Metadata */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle>Metadata</CardTitle>
            <Button variant="outline" size="sm" onClick={openAddMetadata}>
              <Plus className="h-4 w-4" />
              Add
            </Button>
          </CardHeader>
          <CardContent>
            {instance.metadata && Object.keys(instance.metadata).length > 0 ? (
              <div className="space-y-2">
                {Object.entries(instance.metadata).map(([k, v]) => (
                  <div key={k} className="flex items-start justify-between gap-2 rounded border p-2 text-sm">
                    <div className="min-w-0 flex-1">
                      <div className="font-mono font-medium text-xs text-muted-foreground">{k}</div>
                      <div className="mt-0.5 break-all font-mono text-xs whitespace-pre-wrap">{v}</div>
                    </div>
                    <div className="flex shrink-0 gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 w-6 p-0"
                        onClick={() => openEditMetadata(k, v)}
                      >
                        <Pencil className="h-3 w-3" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 w-6 p-0 text-destructive hover:text-destructive"
                        onClick={() => deleteMetadataKeyMutation.mutate(k)}
                        disabled={deleteMetadataKeyMutation.isPending}
                      >
                        <Trash2 className="h-3 w-3" />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">No metadata.</p>
            )}
          </CardContent>
        </Card>

        {/* Metadata add/edit dialog */}
        <Dialog open={metadataDialogOpen} onOpenChange={setMetadataDialogOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{editingKey ? 'Edit Metadata' : 'Add Metadata'}</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="meta-key">Key</Label>
                <Input
                  id="meta-key"
                  value={metaKey}
                  onChange={(e) => setMetaKey(e.target.value)}
                  disabled={!!editingKey}
                  placeholder="e.g. environment"
                  className="font-mono"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="meta-value">Value</Label>
                <textarea
                  id="meta-value"
                  value={metaValue}
                  onChange={(e) => setMetaValue(e.target.value)}
                  placeholder={'Plain string, or JSON/YAML:\n{"key": "value"}\n---\nkey: value'}
                  rows={6}
                  className="w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
                />
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setMetadataDialogOpen(false)}>Cancel</Button>
              <Button
                onClick={() => setMetadataKeyMutation.mutate({ key: metaKey, value: metaValue })}
                disabled={!metaKey.trim() || setMetadataKeyMutation.isPending}
              >
                {editingKey ? 'Save' : 'Add'}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Resize dialog */}
        <Dialog open={resizeDialogOpen} onOpenChange={setResizeDialogOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Resize Instance</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <p className="text-sm text-muted-foreground">
                Select a new flavor. CPU and RAM changes are applied live where the hypervisor
                supports hot-add; otherwise they take effect on the next boot.
              </p>
              <div className="space-y-1.5">
                <Label>Flavor</Label>
                <div className="space-y-1 max-h-64 overflow-y-auto rounded border p-1">
                  {(allFlavors as Flavor[]).map((f) => (
                    <button
                      key={f.id}
                      type="button"
                      onClick={() => setSelectedFlavorId(f.id)}
                      className={[
                        'w-full rounded px-3 py-2 text-left text-sm transition-colors',
                        selectedFlavorId === f.id
                          ? 'bg-primary text-primary-foreground'
                          : 'hover:bg-muted',
                      ].join(' ')}
                    >
                      <span className="font-medium">{f.name}</span>
                      <span className="ml-2 text-xs opacity-70">
                        {f.vcpus} vCPU · {f.ram_mb >= 1024 ? `${f.ram_mb / 1024} GB` : `${f.ram_mb} MB`} RAM · {f.disk_gb} GB disk
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setResizeDialogOpen(false)}>Cancel</Button>
              <Button
                onClick={() => resizeMutation.mutate(selectedFlavorId)}
                disabled={!selectedFlavorId || selectedFlavorId === instance.flavor_id || resizeMutation.isPending}
              >
                {resizeMutation.isPending ? 'Resizing…' : 'Resize'}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Network Interfaces */}
        <NetworkInterfacesCard
          ports={instancePorts}
          floatingIPsByPort={floatingIPsByPort}
          allNetworks={allNetworks as Network[]}
          allPorts={(allPorts ?? []) as Port[]}
          onAttach={(req) => attachInterfaceMutation.mutate(req)}
          onDetach={(portId) => detachInterfaceMutation.mutate({ portId })}
          attaching={attachInterfaceMutation.isPending}
          detaching={detachInterfaceMutation.isPending}
        />

        {/* Security Groups */}
        <SecurityGroupsCard
          ports={instancePorts}
          allSecurityGroups={allSecurityGroups}
          onUpdatePortSGs={(portId, sgIds) => updateSGsMutation.mutate({ portId, sgIds })}
          saving={updateSGsMutation.isPending}
        />

        {/* Volumes */}
        <VolumesCard
          instanceId={instance.id}
          allVolumes={allVolumes as Volume[]}
          onAttach={(volumeId, devicePath) =>
            volumeActionMutation.mutate({
              volumeId,
              req: { action: 'attach', instance_id: instance.id, device_path: devicePath },
            })
          }
          onDetach={(volumeId) =>
            volumeActionMutation.mutate({ volumeId, req: { action: 'detach' } })
          }
          saving={volumeActionMutation.isPending}
        />
      </div>
    </div>
  )
}

function SecurityGroupsCard({
  ports,
  allSecurityGroups,
  onUpdatePortSGs,
  saving,
}: {
  ports: Port[]
  allSecurityGroups: SecurityGroup[]
  onUpdatePortSGs: (portId: string, sgIds: string[]) => void
  saving: boolean
}) {
  const [editPort, setEditPort] = useState<Port | null>(null)

  // Collect unique SG IDs across all ports, track which port(s) each SG is on
  const sgToPortNames = new Map<string, string[]>()
  for (const port of ports) {
    for (const sgId of port.security_group_ids ?? []) {
      const names = sgToPortNames.get(sgId) ?? []
      names.push(port.name || port.id.slice(0, 8))
      sgToPortNames.set(sgId, names)
    }
  }

  const sgById = new Map<string, SecurityGroup>(allSecurityGroups.map((sg) => [sg.id, sg]))
  const attachedSGs = [...sgToPortNames.keys()]
    .map((id) => sgById.get(id))
    .filter((sg): sg is SecurityGroup => sg != null)

  return (
    <>
      <Card className="lg:col-span-2">
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="h-4 w-4" />
              Security Groups
            </CardTitle>
            {ports.length === 1 && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => setEditPort(ports[0])}
              >
                <Pencil className="h-3.5 w-3.5" />
                Edit
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent>
          {attachedSGs.length === 0 ? (
            <div className="flex items-center justify-between">
              <p className="text-sm text-muted-foreground">No security groups applied.</p>
              {ports.length > 1 && (
                <div className="flex gap-2">
                  {ports.map((port) => (
                    <Button key={port.id} size="sm" variant="outline" onClick={() => setEditPort(port)}>
                      <Pencil className="h-3.5 w-3.5" />
                      {port.name || port.id.slice(0, 8)}
                    </Button>
                  ))}
                </div>
              )}
            </div>
          ) : (
            <div className="space-y-4">
              {/* Per-port edit buttons when multiple ports */}
              {ports.length > 1 && (
                <div className="flex flex-wrap gap-2 pb-2 border-b">
                  {ports.map((port) => (
                    <Button key={port.id} size="sm" variant="outline" onClick={() => setEditPort(port)}>
                      <Pencil className="h-3.5 w-3.5" />
                      Edit {port.name || port.id.slice(0, 8)}
                    </Button>
                  ))}
                </div>
              )}
              {attachedSGs.map((sg) => {
                const portLabels = sgToPortNames.get(sg.id) ?? []
                return (
                  <div key={sg.id} className="rounded-md border p-3 text-sm space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 font-medium">
                        <ShieldCheck className="h-4 w-4 text-muted-foreground" />
                        {sg.name}
                        {sg.description && (
                          <span className="text-xs text-muted-foreground font-normal">{sg.description}</span>
                        )}
                      </div>
                      {ports.length > 1 && (
                        <span className="text-xs text-muted-foreground">
                          on: {portLabels.join(', ')}
                        </span>
                      )}
                    </div>
                    {sg.rules && sg.rules.length > 0 ? (
                      <div className="rounded border overflow-hidden">
                        <table className="w-full text-xs">
                          <thead className="bg-muted/50">
                            <tr>
                              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Dir</th>
                              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Proto</th>
                              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Ports</th>
                              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Remote</th>
                              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Ether</th>
                            </tr>
                          </thead>
                          <tbody className="divide-y">
                            {sg.rules.map((rule) => {
                              const portRange =
                                rule.port_range_min == null
                                  ? 'any'
                                  : rule.port_range_min === rule.port_range_max
                                  ? String(rule.port_range_min)
                                  : `${rule.port_range_min}–${rule.port_range_max}`
                              const remote = rule.remote_group_id
                                ? `sg:${rule.remote_group_id.slice(0, 8)}`
                                : (rule.remote_ip_prefix ?? '0.0.0.0/0')
                              return (
                                <tr key={rule.id} className="hover:bg-muted/30">
                                  <td className="px-2 py-1.5">
                                    <span className={[
                                      'inline-flex items-center rounded-full px-1.5 py-0.5 text-xs font-semibold',
                                      rule.direction === 'ingress'
                                        ? 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200'
                                        : 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
                                    ].join(' ')}>
                                      {rule.direction}
                                    </span>
                                  </td>
                                  <td className="px-2 py-1.5 font-mono">{rule.protocol ?? 'any'}</td>
                                  <td className="px-2 py-1.5 font-mono">{portRange}</td>
                                  <td className="px-2 py-1.5 font-mono">{remote}</td>
                                  <td className="px-2 py-1.5">{rule.ethertype}</td>
                                </tr>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>
                    ) : (
                      <p className="text-xs text-muted-foreground">No rules defined.</p>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {editPort && (
        <EditPortSGsDialog
          open={!!editPort}
          onOpenChange={(open) => !open && setEditPort(null)}
          port={editPort}
          allSecurityGroups={allSecurityGroups}
          onSave={(portId, sgIds) => {
            onUpdatePortSGs(portId, sgIds)
            setEditPort(null)
          }}
          saving={saving}
        />
      )}
    </>
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

  const portLabel = port.name || port.id.slice(0, 8)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Edit Security Groups — {portLabel}</DialogTitle>
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

// ─── Network interfaces card ──────────────────────────────────────────────────

function NetworkInterfacesCard({
  ports,
  floatingIPsByPort,
  allNetworks,
  allPorts,
  onAttach,
  onDetach,
  attaching,
  detaching,
}: {
  ports: Port[]
  floatingIPsByPort: Map<string, FloatingIP>
  allNetworks: Network[]
  allPorts: Port[]
  onAttach: (req: { network_id?: string; port_id?: string }) => void
  onDetach: (portId: string) => void
  attaching: boolean
  detaching: boolean
}) {
  const [attachOpen, setAttachOpen] = useState(false)
  const [detachTarget, setDetachTarget] = useState<Port | null>(null)

  // Only offer non-external networks for attach
  const tenantNetworks = allNetworks.filter((n) => !n.external)

  // Ports not attached to any device and not already on this instance
  const attachedPortIds = new Set(ports.map((p) => p.id))
  const freePorts = allPorts.filter((p) => !p.device_id && !attachedPortIds.has(p.id))

  return (
    <>
      <Card className="lg:col-span-2">
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="flex items-center gap-2">
              <NetworkIcon className="h-4 w-4" />
              Network Interfaces
            </CardTitle>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setAttachOpen(true)}
              disabled={(tenantNetworks.length === 0 && freePorts.length === 0) || attaching}
            >
              <Plug className="h-3.5 w-3.5" />
              Attach Interface
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {ports.length === 0 ? (
            <p className="text-sm text-muted-foreground">No network interfaces attached.</p>
          ) : (
            <div className="space-y-3">
              {ports.map((port) => {
                const fip = floatingIPsByPort.get(port.id)
                const network = allNetworks.find((n) => n.id === port.network_id)
                return (
                  <div key={port.id} className="rounded-md border p-3 text-sm space-y-2">
                    <div className="flex items-center justify-between gap-4">
                      <div className="flex items-center gap-2 min-w-0">
                        <span className="font-mono text-xs text-muted-foreground truncate">{port.id}</span>
                        {network && (
                          <span className="text-xs text-muted-foreground shrink-0">({network.name})</span>
                        )}
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <StatusBadge status={port.status} />
                        <Button
                          size="sm"
                          variant="ghost"
                          title="Detach interface"
                          disabled={detaching}
                          onClick={() => setDetachTarget(port)}
                        >
                          <Unplug className="h-4 w-4 text-muted-foreground" />
                        </Button>
                      </div>
                    </div>
                    <dl className="grid grid-cols-2 gap-x-6 gap-y-2">
                      <DetailRow label="MAC Address" value={port.mac_address} mono />
                      <DetailRow label="Network ID" value={port.network_id} />
                      {port.name && <DetailRow label="Name" value={port.name} />}
                      {(port.fixed_ips ?? []).map((ip, i) => (
                        <DetailRow
                          key={i}
                          label={`IP Address${(port.fixed_ips?.length ?? 0) > 1 ? ` ${i + 1}` : ''}`}
                          value={ip.ip_address}
                          mono
                        />
                      ))}
                    </dl>
                    {fip && (
                      <div className="flex items-center gap-2 rounded-md bg-blue-50 dark:bg-blue-950/30 border border-blue-200 dark:border-blue-800 px-3 py-2">
                        <Globe className="h-3.5 w-3.5 text-blue-600 dark:text-blue-400 shrink-0" />
                        <span className="text-xs text-blue-700 dark:text-blue-300 font-medium">Floating IP</span>
                        <span className="font-mono text-xs text-blue-900 dark:text-blue-100 ml-1">{fip.floating_ip_address}</span>
                        <span className="ml-auto text-xs text-blue-500 dark:text-blue-400 font-mono">{fip.id.slice(0, 8)}</span>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <AttachInterfaceDialog
        open={attachOpen}
        onOpenChange={setAttachOpen}
        networks={tenantNetworks}
        freePorts={freePorts}
        onConfirm={(req) => {
          onAttach(req)
          setAttachOpen(false)
        }}
        saving={attaching}
      />

      <ConfirmDialog
        open={!!detachTarget}
        onOpenChange={(open) => !open && setDetachTarget(null)}
        title="Detach Interface"
        description={`Remove port ${detachTarget?.id.slice(0, 8)} from this instance? Any floating IP on this port will be disassociated.`}
        onConfirm={() => {
          if (detachTarget) onDetach(detachTarget.id)
          setDetachTarget(null)
        }}
        loading={detaching}
        destructive
      />
    </>
  )
}

function AttachInterfaceDialog({
  open,
  onOpenChange,
  networks,
  freePorts,
  onConfirm,
  saving,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  networks: Network[]
  freePorts: Port[]
  onConfirm: (req: { network_id?: string; port_id?: string }) => void
  saving: boolean
}) {
  const [mode, setMode] = useState<'new' | 'existing'>('new')
  const [networkId, setNetworkId] = useState('')
  const [portId, setPortId] = useState('')

  useEffect(() => {
    if (open) {
      setMode('new')
      setNetworkId(networks[0]?.id ?? '')
      setPortId(freePorts[0]?.id ?? '')
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  const canConfirm = mode === 'new' ? !!networkId : !!portId

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Attach Network Interface</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="flex rounded-md border overflow-hidden text-sm">
            <button
              type="button"
              className={[
                'flex-1 px-3 py-2 transition-colors',
                mode === 'new' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted/50',
              ].join(' ')}
              onClick={() => setMode('new')}
            >
              New Port
            </button>
            <button
              type="button"
              className={[
                'flex-1 px-3 py-2 transition-colors border-l',
                mode === 'existing' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted/50',
                freePorts.length === 0 ? 'opacity-40 cursor-not-allowed' : '',
              ].join(' ')}
              onClick={() => freePorts.length > 0 && setMode('existing')}
            >
              Existing Port
            </button>
          </div>

          {mode === 'new' ? (
            <div className="space-y-1.5">
              <Label>Network</Label>
              <select
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                value={networkId}
                onChange={(e) => setNetworkId(e.target.value)}
              >
                {networks.map((n) => (
                  <option key={n.id} value={n.id}>{n.name}</option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                A new port will be created on the selected network.
              </p>
            </div>
          ) : (
            <div className="space-y-1.5">
              <Label>Port</Label>
              <select
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                value={portId}
                onChange={(e) => setPortId(e.target.value)}
              >
                {freePorts.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name || p.id.slice(0, 12)}
                    {p.fixed_ips?.[0]?.ip_address ? ` — ${p.fixed_ips[0].ip_address}` : ''}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Attach an existing unattached port to this instance.
              </p>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button
            onClick={() => onConfirm(mode === 'new' ? { network_id: networkId } : { port_id: portId })}
            disabled={!canConfirm || saving}
          >
            {saving ? 'Attaching…' : 'Attach'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Volumes card ─────────────────────────────────────────────────────────────

function VolumesCard({
  instanceId,
  allVolumes,
  onAttach,
  onDetach,
  saving,
}: {
  instanceId: string
  allVolumes: Volume[]
  onAttach: (volumeId: string, devicePath: string) => void
  onDetach: (volumeId: string) => void
  saving: boolean
}) {
  const [attachOpen, setAttachOpen] = useState(false)
  const [detachTarget, setDetachTarget] = useState<Volume | null>(null)

  const attached = allVolumes.filter((v) => v.attached_to === instanceId)
  const available = allVolumes.filter((v) => v.status === 'available')

  return (
    <>
      <Card className="lg:col-span-2">
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="flex items-center gap-2">
              <HardDrive className="h-4 w-4" />
              Volumes
            </CardTitle>
            <Button size="sm" variant="outline" onClick={() => setAttachOpen(true)} disabled={available.length === 0}>
              <Plug className="h-3.5 w-3.5" />
              Attach Volume
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {attached.length === 0 ? (
            <p className="text-sm text-muted-foreground">No volumes attached.</p>
          ) : (
            <div className="space-y-2">
              {attached.map((vol) => (
                <div key={vol.id} className="flex items-center justify-between rounded-md border px-3 py-2 text-sm gap-4">
                  <div className="flex items-center gap-3 min-w-0">
                    <HardDrive className="h-4 w-4 text-muted-foreground shrink-0" />
                    <div className="min-w-0">
                      <span className="font-medium">{vol.name}</span>
                      <span className="ml-2 text-muted-foreground text-xs">{vol.size_gb} GB</span>
                      {vol.device_path && (
                        <span className="ml-2 font-mono text-xs text-muted-foreground">{vol.device_path}</span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <StatusBadge status={vol.status} />
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setDetachTarget(vol)}
                      disabled={saving}
                      title="Detach"
                    >
                      <Unplug className="h-4 w-4 text-muted-foreground" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <AttachVolumeDialog
        open={attachOpen}
        onOpenChange={setAttachOpen}
        availableVolumes={available}
        onConfirm={(volumeId, devicePath) => {
          onAttach(volumeId, devicePath)
          setAttachOpen(false)
        }}
        saving={saving}
      />

      <ConfirmDialog
        open={!!detachTarget}
        onOpenChange={(open) => !open && setDetachTarget(null)}
        title="Detach Volume"
        description={`Detach "${detachTarget?.name}" from this instance?`}
        onConfirm={() => {
          if (detachTarget) onDetach(detachTarget.id)
          setDetachTarget(null)
        }}
        loading={saving}
      />
    </>
  )
}

function AttachVolumeDialog({
  open,
  onOpenChange,
  availableVolumes,
  onConfirm,
  saving,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  availableVolumes: Volume[]
  onConfirm: (volumeId: string, devicePath: string) => void
  saving: boolean
}) {
  const [volumeId, setVolumeId] = useState('')
  const [devicePath, setDevicePath] = useState('/dev/vdb')

  useEffect(() => {
    if (open) {
      setVolumeId(availableVolumes[0]?.id ?? '')
      setDevicePath('/dev/vdb')
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Attach Volume</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label>Volume</Label>
            <select
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={volumeId}
              onChange={(e) => setVolumeId(e.target.value)}
            >
              {availableVolumes.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.name} ({v.size_gb} GB)
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label>Guest Device Path</Label>
            <Input
              placeholder="/dev/vdb"
              value={devicePath}
              onChange={(e) => setDevicePath(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">Device name as it will appear inside the instance.</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>Cancel</Button>
          <Button
            onClick={() => onConfirm(volumeId, devicePath)}
            disabled={!volumeId || !devicePath || saving}
          >
            {saving ? 'Attaching…' : 'Attach'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DetailRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex justify-between gap-4">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? 'font-mono text-xs text-right break-all' : 'font-medium text-right break-all'}>{value}</dd>
    </div>
  )
}
