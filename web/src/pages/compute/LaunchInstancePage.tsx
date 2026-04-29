import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, ChevronLeft, Server, HardDrive, Network, Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from '@/hooks/use-toast'
import { listFlavors, createInstance, listKeypairs } from '@/api/compute'
import { listImages } from '@/api/images'
import { listNetworks, listSubnets, listPorts, listFloatingIPs, updateFloatingIP } from '@/api/network'
import { listVolumes } from '@/api/storage'
import type { CreateInstanceRequest } from '@/types/compute'
import type { Subnet, Port, FloatingIP } from '@/types/network'

// ── Wizard step definitions ───────────────────────────────────────────────────

const STEPS = [
  { id: 'info',    label: 'Instance Info',  icon: Server },
  { id: 'storage', label: 'Storage & Config', icon: HardDrive },
  { id: 'network', label: 'Networking',     icon: Network },
] as const

type StepId = typeof STEPS[number]['id']

// ── Form state ────────────────────────────────────────────────────────────────

interface NetworkAttachment {
  /** 'auto' = let controller create port on selected network
   *  'port' = use an existing port
   */
  mode: 'auto' | 'port'
  networkId: string
  portId: string        // used when mode === 'port'
  fixedIp: string       // optional fixed IP hint
  subnetId: string      // optional subnet hint
  associateFloatingIp: boolean
  floatingIpId: string  // pre-existing unassociated FIP to attach
}

interface WizardForm {
  // Step 1
  name: string
  imageId: string
  bootVolumeId: string         // mutually exclusive with imageId
  deleteBootVolume: boolean
  flavorId: string
  hypervisorType: string
  keyNames: string[]
  // Step 2
  userData: string
  // Step 3
  networks: NetworkAttachment[]
}

const emptyNetwork = (): NetworkAttachment => ({
  mode: 'auto',
  networkId: '',
  portId: '',
  fixedIp: '',
  subnetId: '',
  associateFloatingIp: false,
  floatingIpId: '',
})

const defaultForm = (): WizardForm => ({
  name: '',
  imageId: '',
  bootVolumeId: '',
  deleteBootVolume: false,
  flavorId: '',
  hypervisorType: 'kvm',
  keyNames: [],
  userData: '',
  networks: [emptyNetwork()],
})

// ── Page component ────────────────────────────────────────────────────────────

export default function LaunchInstancePage() {
  const navigate = useNavigate()
  const qc = useQueryClient()

  const [step, setStep] = useState<StepId>('info')
  const [form, setForm] = useState<WizardForm>(defaultForm)

  const { data: flavors = [] }    = useQuery({ queryKey: ['flavors'],    queryFn: listFlavors })
  const { data: images = [] }     = useQuery({ queryKey: ['images'],     queryFn: listImages })
  const { data: networks = [] }   = useQuery({ queryKey: ['networks'],   queryFn: listNetworks })
  const { data: subnets = [] }    = useQuery({ queryKey: ['subnets'],    queryFn: listSubnets })
  const { data: ports = [] }      = useQuery({ queryKey: ['ports'],      queryFn: listPorts })
  const { data: keypairs = [] }   = useQuery({ queryKey: ['keypairs'],   queryFn: listKeypairs })
  const { data: floatingIps = [] } = useQuery({ queryKey: ['floatingips'], queryFn: listFloatingIPs })
  const { data: volumes = [] }    = useQuery({ queryKey: ['volumes'],    queryFn: listVolumes })

  // Unassociated FIPs available for attachment
  const freeFIPs = floatingIps.filter((f: FloatingIP) => !f.port_id)

  const associateMutation = useMutation({
    mutationFn: ({ fipId, portId }: { fipId: string; portId: string }) =>
      updateFloatingIP(fipId, { port_id: portId }),
  })

  const createMutation = useMutation({
    mutationFn: (req: CreateInstanceRequest) => createInstance(req),
    onSuccess: async (inst) => {
      qc.invalidateQueries({ queryKey: ['instances'] })
      toast({ title: 'Instance launched', description: 'Provisioning has started.' })

      // Post-launch: associate any requested floating IPs once we know the port IDs.
      // The instance ports are created by the controller during scheduleAndBoot;
      // we store the FIP<->network-index mapping and associate after a short delay.
      // For simplicity we associate immediately — the controller has already created
      // the port synchronously before responding.
      for (const net of form.networks) {
        if (!net.associateFloatingIp || !net.floatingIpId) continue
        // Find the port that was created for this network on this instance.
        const instPort = inst.port_ids?.[0]
        if (instPort) {
          await associateMutation.mutateAsync({ fipId: net.floatingIpId, portId: instPort })
        }
      }

      navigate('/compute/instances')
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Launch failed', description: err.message })
    },
  })

  function buildRequest(): CreateInstanceRequest {
    const networkRequests = form.networks
      .filter((n) => n.networkId || n.portId)
      .map((n) => {
        if (n.mode === 'port' && n.portId) return { network_id: n.networkId, port_id: n.portId }
        return { network_id: n.networkId }
      })

    const req: CreateInstanceRequest = {
      name: form.name,
      flavor_id: form.flavorId,
      hypervisor_type: form.hypervisorType || undefined,
      networks: networkRequests,
      user_data: form.userData || undefined,
      key_names: form.keyNames.length ? form.keyNames : undefined,
    }

    if (form.bootVolumeId) {
      req.block_device_mappings = [{
        volume_id: form.bootVolumeId,
        delete_on_terminate: form.deleteBootVolume,
      }]
    } else {
      req.image_id = form.imageId
    }

    return req
  }

  const stepIndex = STEPS.findIndex((s) => s.id === step)

  function canAdvance(): boolean {
    if (step === 'info') return !!form.name && (!!form.imageId || !!form.bootVolumeId) && !!form.flavorId
    if (step === 'storage') return true
    return true
  }

  function advance() {
    if (step === 'network') {
      createMutation.mutate(buildRequest())
      return
    }
    setStep(STEPS[stepIndex + 1].id)
  }

  function back() {
    if (stepIndex === 0) { navigate('/compute/instances'); return }
    setStep(STEPS[stepIndex - 1].id)
  }

  function patchForm(patch: Partial<WizardForm>) {
    setForm((f) => ({ ...f, ...patch }))
  }

  function patchNetwork(idx: number, patch: Partial<NetworkAttachment>) {
    setForm((f) => {
      const networks = [...f.networks]
      networks[idx] = { ...networks[idx], ...patch }
      return { ...f, networks }
    })
  }

  const selectedFlavor = flavors.find((f) => f.id === form.flavorId)
  const selectedImage  = images.find((i) => i.id === form.imageId)

  return (
    <div className="max-w-3xl mx-auto">
      {/* ── Page header ── */}
      <div className="mb-8">
        <h1 className="text-xl font-semibold tracking-tight text-foreground">Launch Instance</h1>
        <p className="mt-1 text-sm text-muted-foreground">Configure and provision a new virtual machine.</p>
      </div>

      {/* ── Step indicator ── */}
      <StepIndicator steps={STEPS} current={step} />

      {/* ── Step content ── */}
      <div className="mt-8 rounded-lg border border-border bg-card p-6">
        {step === 'info' && (
          <StepInfo
            form={form}
            flavors={flavors}
            images={images}
            bootVolumes={(volumes as Array<{ id: string; name: string; size_gb: number; status: string; bootable?: boolean }>)
              .filter((v) => v.bootable && v.status === 'available')}
            keypairs={keypairs.map((k) => ({ name: k.name, fingerprint: k.fingerprint }))}
            onChange={patchForm}
          />
        )}
        {step === 'storage' && (
          <StepStorage form={form} onChange={patchForm} />
        )}
        {step === 'network' && (
          <StepNetwork
            form={form}
            networks={networks}
            subnets={subnets}
            ports={ports}
            freeFIPs={freeFIPs}
            onChange={patchForm}
            onPatchNetwork={patchNetwork}
          />
        )}
      </div>

      {/* ── Navigation ── */}
      <div className="mt-6 flex items-center justify-between">
        <Button variant="outline" onClick={back} disabled={createMutation.isPending}>
          <ChevronLeft className="h-4 w-4 mr-1" />
          {stepIndex === 0 ? 'Cancel' : 'Back'}
        </Button>

        {/* Mini summary while stepping */}
        {(selectedFlavor || selectedImage) && (
          <span className="text-xs text-muted-foreground hidden sm:block">
            {form.name && <span className="font-medium text-foreground">{form.name}</span>}
            {selectedImage && <span> · {selectedImage.name}</span>}
            {selectedFlavor && <span> · {selectedFlavor.name}</span>}
          </span>
        )}

        <Button
          onClick={advance}
          disabled={!canAdvance() || createMutation.isPending}
        >
          {step === 'network' ? (
            createMutation.isPending ? 'Launching…' : (
              <>
                <Check className="h-4 w-4 mr-1" />
                Launch
              </>
            )
          ) : (
            <>
              Next
              <ChevronRight className="h-4 w-4 ml-1" />
            </>
          )}
        </Button>
      </div>
    </div>
  )
}

// ── Step indicator ────────────────────────────────────────────────────────────

function StepIndicator({
  steps,
  current,
}: {
  steps: typeof STEPS
  current: StepId
}) {
  const currentIdx = steps.findIndex((s) => s.id === current)
  return (
    <nav className="flex items-center gap-0">
      {steps.map((s, i) => {
        const done    = i < currentIdx
        const active  = s.id === current
        const Icon    = s.icon
        return (
          <div key={s.id} className="flex items-center flex-1">
            <div className={`flex items-center gap-2 px-4 py-2.5 rounded-md text-sm font-medium transition-colors
              ${active  ? 'bg-primary/10 text-primary' : ''}
              ${done    ? 'text-muted-foreground' : ''}
              ${!active && !done ? 'text-muted-foreground/50' : ''}
            `}>
              {done ? (
                <span className="flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-foreground text-xs">
                  <Check className="h-3 w-3" />
                </span>
              ) : (
                <span className={`flex h-5 w-5 items-center justify-center rounded-full text-xs font-semibold
                  ${active ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'}`}>
                  {i + 1}
                </span>
              )}
              <Icon className="h-4 w-4" />
              {s.label}
            </div>
            {i < steps.length - 1 && (
              <ChevronRight className="h-4 w-4 text-border mx-1 shrink-0" />
            )}
          </div>
        )
      })}
    </nav>
  )
}

// ── Step 1: Instance Info ─────────────────────────────────────────────────────

function StepInfo({
  form,
  flavors,
  images,
  bootVolumes,
  keypairs,
  onChange,
}: {
  form: WizardForm
  flavors: { id: string; name: string; vcpus: number; ram_mb: number; disk_gb: number }[]
  images: { id: string; name: string }[]
  bootVolumes: { id: string; name: string; size_gb: number }[]
  keypairs: { name: string; fingerprint: string }[]
  onChange: (p: Partial<WizardForm>) => void
}) {
  const sourceMode = form.bootVolumeId ? 'volume' : 'image'

  return (
    <div className="space-y-6">
      <SectionTitle>Basic Information</SectionTitle>

      <Field label="Instance Name" required>
        <Input
          value={form.name}
          onChange={(e) => onChange({ name: e.target.value })}
          placeholder="my-instance"
          autoFocus
        />
      </Field>

      <Field label="Hypervisor Type">
        <Select value={form.hypervisorType} onValueChange={(v) => onChange({ hypervisorType: v })}>
          <SelectTrigger><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="kvm">KVM (hardware-accelerated)</SelectItem>
            <SelectItem value="lxd">LXD (container)</SelectItem>
            <SelectItem value="lxc">LXC (container)</SelectItem>
            <SelectItem value="containerd">containerd</SelectItem>
          </SelectContent>
        </Select>
      </Field>

      <SectionTitle>Boot Source</SectionTitle>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => onChange({ bootVolumeId: '', deleteBootVolume: false })}
          className={`flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors ${
            sourceMode === 'image'
              ? 'border-primary bg-primary/10 text-primary'
              : 'border-border text-muted-foreground hover:border-muted-foreground'
          }`}
        >
          Image
        </button>
        {bootVolumes.length > 0 && (
          <button
            type="button"
            onClick={() => onChange({ imageId: '' })}
            className={`flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors ${
              sourceMode === 'volume'
                ? 'border-primary bg-primary/10 text-primary'
                : 'border-border text-muted-foreground hover:border-muted-foreground'
            }`}
          >
            Boot Volume
          </button>
        )}
      </div>

      {sourceMode === 'image' && (
        <div className="grid grid-cols-1 gap-2">
          {images.length === 0 && (
            <p className="text-sm text-muted-foreground">No images available.</p>
          )}
          {images.map((img) => (
            <SelectCard
              key={img.id}
              selected={form.imageId === img.id}
              onClick={() => onChange({ imageId: img.id })}
            >
              <span className="font-medium text-sm">{img.name}</span>
              <span className="text-xs text-muted-foreground font-mono ml-auto">{img.id.slice(0, 8)}</span>
            </SelectCard>
          ))}
        </div>
      )}

      {sourceMode === 'volume' && (
        <div className="space-y-3">
          <div className="grid grid-cols-1 gap-2">
            {bootVolumes.map((vol) => (
              <SelectCard
                key={vol.id}
                selected={form.bootVolumeId === vol.id}
                onClick={() => onChange({ bootVolumeId: vol.id })}
              >
                <span className="font-medium text-sm">{vol.name}</span>
                <span className="text-xs text-muted-foreground ml-auto">{vol.size_gb} GB</span>
              </SelectCard>
            ))}
          </div>
          {form.bootVolumeId && (
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-input accent-primary"
                checked={form.deleteBootVolume}
                onChange={(e) => onChange({ deleteBootVolume: e.target.checked })}
              />
              Delete boot volume when instance is deleted
            </label>
          )}
        </div>
      )}

      <SectionTitle>Flavor</SectionTitle>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
        {flavors.map((fl) => (
          <SelectCard
            key={fl.id}
            selected={form.flavorId === fl.id}
            onClick={() => onChange({ flavorId: fl.id })}
          >
            <div>
              <div className="font-medium text-sm">{fl.name}</div>
              <div className="text-xs text-muted-foreground mt-0.5">
                {fl.vcpus} vCPU · {fl.ram_mb >= 1024 ? `${fl.ram_mb / 1024} GB` : `${fl.ram_mb} MB`} RAM · {fl.disk_gb} GB disk
              </div>
            </div>
          </SelectCard>
        ))}
      </div>

      {keypairs.length > 0 && (
        <>
          <SectionTitle>SSH Key Pairs <span className="font-normal text-muted-foreground">(optional)</span></SectionTitle>
          <div className="space-y-1">
            {keypairs.map((kp) => (
              <label key={kp.name} className="flex items-center gap-2 text-sm cursor-pointer rounded-md px-3 py-2 hover:bg-muted/50 border border-transparent has-[:checked]:border-primary has-[:checked]:bg-primary/5 transition-colors">
                <input
                  type="checkbox"
                  className="h-4 w-4 rounded border-input accent-primary"
                  checked={form.keyNames.includes(kp.name)}
                  onChange={(e) => {
                    onChange({
                      keyNames: e.target.checked
                        ? [...form.keyNames, kp.name]
                        : form.keyNames.filter((n) => n !== kp.name),
                    })
                  }}
                />
                <span className="font-medium">{kp.name}</span>
                <code className="text-xs text-muted-foreground ml-auto">{kp.fingerprint}</code>
              </label>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

// ── Step 2: Storage & Configuration ──────────────────────────────────────────

function StepStorage({
  form,
  onChange,
}: {
  form: WizardForm
  onChange: (p: Partial<WizardForm>) => void
}) {
  return (
    <div className="space-y-6">
      <SectionTitle>Cloud-Init User Data <span className="font-normal text-muted-foreground">(optional)</span></SectionTitle>

      <p className="text-sm text-muted-foreground -mt-3">
        Provide a <code className="text-xs bg-muted px-1 py-0.5 rounded">#cloud-config</code> document or a shell script.
        Leave blank to use the image defaults.
      </p>

      <textarea
        value={form.userData}
        onChange={(e) => onChange({ userData: e.target.value })}
        placeholder={'#cloud-config\npackages:\n  - nginx\nruncmd:\n  - systemctl enable --now nginx'}
        rows={14}
        className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono shadow-sm placeholder:text-muted-foreground/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
      />
    </div>
  )
}

// ── Step 3: Network Configuration ────────────────────────────────────────────

function StepNetwork({
  form,
  networks,
  subnets,
  ports,
  freeFIPs,
  onChange,
  onPatchNetwork,
}: {
  form: WizardForm
  networks: { id: string; name: string }[]
  subnets: Subnet[]
  ports: Port[]
  freeFIPs: FloatingIP[]
  onChange: (p: Partial<WizardForm>) => void
  onPatchNetwork: (idx: number, p: Partial<NetworkAttachment>) => void
}) {
  function addNetwork() {
    onChange({ networks: [...form.networks, emptyNetwork()] })
  }

  function removeNetwork(idx: number) {
    onChange({ networks: form.networks.filter((_, i) => i !== idx) })
  }

  return (
    <div className="space-y-6">
      {form.networks.map((net, idx) => {
        const networkSubnets = subnets.filter((s) => s.network_id === net.networkId)
        const networkPorts   = ports.filter((p) => p.network_id === net.networkId && !p.device_id)

        return (
          <div key={idx} className="rounded-md border border-border p-4 space-y-4">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-foreground">Interface {idx + 1}</span>
              {form.networks.length > 1 && (
                <button
                  type="button"
                  onClick={() => removeNetwork(idx)}
                  className="text-xs text-destructive hover:underline"
                >
                  Remove
                </button>
              )}
            </div>

            {/* Network select */}
            <Field label="Network">
              <Select
                value={net.networkId || '__none__'}
                onValueChange={(v) => onPatchNetwork(idx, {
                  networkId: v === '__none__' ? '' : v,
                  portId: '',
                  subnetId: '',
                })}
              >
                <SelectTrigger><SelectValue placeholder="No network" /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">No network</SelectItem>
                  {networks.map((n) => (
                    <SelectItem key={n.id} value={n.id}>{n.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            {net.networkId && (
              <>
                {/* Port allocation mode */}
                <Field label="Port Allocation">
                  <div className="flex rounded-md border border-border overflow-hidden text-sm">
                    <ModeBtn active={net.mode === 'auto'} onClick={() => onPatchNetwork(idx, { mode: 'auto', portId: '' })}>
                      Auto-allocate port
                    </ModeBtn>
                    <ModeBtn active={net.mode === 'port'} onClick={() => onPatchNetwork(idx, { mode: 'port' })}>
                      Use existing port
                    </ModeBtn>
                  </div>
                </Field>

                {net.mode === 'port' && (
                  <Field label="Existing Port">
                    <Select
                      value={net.portId || '__none__'}
                      onValueChange={(v) => onPatchNetwork(idx, { portId: v === '__none__' ? '' : v })}
                    >
                      <SelectTrigger><SelectValue placeholder="Select port" /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">— select —</SelectItem>
                        {networkPorts.length === 0 && (
                          <SelectItem value="__empty__" disabled>No available ports</SelectItem>
                        )}
                        {networkPorts.map((p) => (
                          <SelectItem key={p.id} value={p.id}>
                            {p.name || p.id.slice(0, 12)} · {p.mac_address}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                )}

                {net.mode === 'auto' && networkSubnets.length > 0 && (
                  <>
                    <Field label={<>Subnet <span className="text-muted-foreground font-normal">(optional)</span></>}>
                      <Select
                        value={net.subnetId || '__any__'}
                        onValueChange={(v) => onPatchNetwork(idx, { subnetId: v === '__any__' ? '' : v })}
                      >
                        <SelectTrigger><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="__any__">Any subnet</SelectItem>
                          {networkSubnets.map((s) => (
                            <SelectItem key={s.id} value={s.id}>
                              {s.name || s.cidr} ({s.cidr})
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>

                    <Field label={<>Fixed IP <span className="text-muted-foreground font-normal">(optional)</span></>}>
                      <Input
                        value={net.fixedIp}
                        onChange={(e) => onPatchNetwork(idx, { fixedIp: e.target.value })}
                        placeholder="e.g. 192.168.1.50"
                        className="font-mono text-sm"
                      />
                    </Field>
                  </>
                )}

                {/* Floating IP */}
                <div className="flex items-center gap-2 pt-1">
                  <input
                    id={`fip-toggle-${idx}`}
                    type="checkbox"
                    className="h-4 w-4 rounded border-input accent-primary"
                    checked={net.associateFloatingIp}
                    onChange={(e) => onPatchNetwork(idx, {
                      associateFloatingIp: e.target.checked,
                      floatingIpId: '',
                    })}
                  />
                  <label htmlFor={`fip-toggle-${idx}`} className="text-sm cursor-pointer select-none">
                    Associate a floating IP after launch
                  </label>
                </div>

                {net.associateFloatingIp && (
                  <Field label="Floating IP">
                    {freeFIPs.length === 0 ? (
                      <p className="text-xs text-muted-foreground">No unassociated floating IPs available. Allocate one on the Floating IPs page first.</p>
                    ) : (
                      <Select
                        value={net.floatingIpId || '__none__'}
                        onValueChange={(v) => onPatchNetwork(idx, { floatingIpId: v === '__none__' ? '' : v })}
                      >
                        <SelectTrigger><SelectValue placeholder="Select floating IP" /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="__none__">— select —</SelectItem>
                          {freeFIPs.map((f) => (
                            <SelectItem key={f.id} value={f.id}>
                              {f.floating_ip_address}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                  </Field>
                )}
              </>
            )}
          </div>
        )
      })}

      <Button type="button" variant="outline" size="sm" onClick={addNetwork}>
        + Add Another Interface
      </Button>
    </div>
  )
}

// ── Shared primitives ─────────────────────────────────────────────────────────

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="text-sm font-semibold text-foreground uppercase tracking-wide border-b border-border pb-2">
      {children}
    </h2>
  )
}

function Field({
  label,
  required,
  children,
}: {
  label: React.ReactNode
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-sm">
        {label}
        {required && <span className="text-destructive ml-0.5">*</span>}
      </Label>
      {children}
    </div>
  )
}

function SelectCard({
  selected,
  onClick,
  children,
}: {
  selected: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-3 w-full text-left rounded-md border px-4 py-3 transition-colors text-sm
        ${selected
          ? 'border-primary bg-primary/5 text-foreground'
          : 'border-border bg-background text-foreground hover:bg-muted/40'
        }`}
    >
      {selected && <Check className="h-4 w-4 text-primary shrink-0" />}
      {!selected && <span className="h-4 w-4 shrink-0" />}
      {children}
    </button>
  )
}

function ModeBtn({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex-1 px-3 py-2 text-sm transition-colors
        ${active
          ? 'bg-primary text-primary-foreground'
          : 'bg-card text-muted-foreground hover:bg-muted'
        }`}
    >
      {children}
    </button>
  )
}
