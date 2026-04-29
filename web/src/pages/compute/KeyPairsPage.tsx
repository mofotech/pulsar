import { useState, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, Copy, Download, KeyRound } from 'lucide-react'
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
import { toast } from '@/hooks/use-toast'
import { listKeypairs, createKeypair, deleteKeypair } from '@/api/compute'
import type { KeyPair } from '@/types/compute'

export default function KeyPairsPage() {
  const qc = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<KeyPair | null>(null)
  const [generatedKey, setGeneratedKey] = useState<{ name: string; pem: string } | null>(null)

  const { data: keypairs = [], isLoading, error } = useQuery({
    queryKey: ['keypairs'],
    queryFn: listKeypairs,
  })

  const createMutation = useMutation({
    mutationFn: createKeypair,
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['keypairs'] })
      setAddOpen(false)
      if (res.private_key) {
        setGeneratedKey({ name: res.keypair.name, pem: res.private_key })
      } else {
        toast({ title: 'Key pair imported' })
      }
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Failed to add key pair', description: err.message })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteKeypair,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['keypairs'] })
      setDeleteTarget(null)
      toast({ title: 'Key pair deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<KeyPair>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (row) => (
        <span className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground shrink-0" />
          {row.name}
        </span>
      ),
    },
    { key: 'fingerprint', header: 'Fingerprint', render: (row) => (
      <code className="text-xs font-mono text-muted-foreground">{row.fingerprint}</code>
    )},
    { key: 'created_at', header: 'Created', render: (row) =>
      new Date(row.created_at).toLocaleString()
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
        title="Key Pairs"
        description="SSH key pairs for instance access"
        action={
          <Button onClick={() => setAddOpen(true)}>
            <Plus className="h-4 w-4" />
            Add Key Pair
          </Button>
        }
      />

      <ResourceTable
        columns={columns}
        data={keypairs}
        isLoading={isLoading}
        error={error}
        emptyMessage="No key pairs. Add one to inject SSH access into new instances."
      />

      <AddKeyPairDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        onSubmit={(req) => createMutation.mutate(req)}
        loading={createMutation.isPending}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Key Pair"
        description={`Delete key pair "${deleteTarget?.name}"? Existing instances will not be affected.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />

      {generatedKey && (
        <PrivateKeyModal
          name={generatedKey.name}
          pem={generatedKey.pem}
          onClose={() => setGeneratedKey(null)}
        />
      )}
    </div>
  )
}

// ── Add Key Pair Dialog ───────────────────────────────────────────────────────

function AddKeyPairDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (req: { name: string; public_key?: string }) => void
  loading: boolean
}) {
  const [mode, setMode] = useState<'import' | 'generate'>('import')
  const [name, setName] = useState('')
  const [publicKey, setPublicKey] = useState('')

  function reset() {
    setMode('import')
    setName('')
    setPublicKey('')
  }

  function handleOpenChange(open: boolean) {
    if (!open) reset()
    onOpenChange(open)
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    onSubmit({
      name,
      public_key: mode === 'import' ? publicKey.trim() : undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add Key Pair</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Mode toggle */}
          <div className="flex rounded-md border border-border overflow-hidden text-sm">
            <button
              type="button"
              onClick={() => setMode('import')}
              className={`flex-1 px-4 py-2 transition-colors ${
                mode === 'import'
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-card text-muted-foreground hover:bg-muted'
              }`}
            >
              Import public key
            </button>
            <button
              type="button"
              onClick={() => setMode('generate')}
              className={`flex-1 px-4 py-2 transition-colors ${
                mode === 'generate'
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-card text-muted-foreground hover:bg-muted'
              }`}
            >
              Generate key pair
            </button>
          </div>

          <div className="space-y-2">
            <Label htmlFor="kp-name">Key Pair Name</Label>
            <Input
              id="kp-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-key"
              required
            />
          </div>

          {mode === 'import' && (
            <div className="space-y-2">
              <Label htmlFor="kp-pubkey">Public Key</Label>
              <textarea
                id="kp-pubkey"
                value={publicKey}
                onChange={(e) => setPublicKey(e.target.value)}
                placeholder="ssh-rsa AAAA... user@host"
                required
                rows={4}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono resize-none focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                Paste the contents of your <code>~/.ssh/id_rsa.pub</code> or similar file.
              </p>
            </div>
          )}

          {mode === 'generate' && (
            <p className="text-sm text-muted-foreground rounded-md border border-border bg-muted/40 px-3 py-2">
              Pulsar will generate an RSA-4096 key pair. The private key will be shown
              <strong> once</strong> — download it immediately. The public key is stored on
              the server.
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => handleOpenChange(false)} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              {loading
                ? mode === 'import' ? 'Importing...' : 'Generating...'
                : mode === 'import' ? 'Import' : 'Generate'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ── Private Key Modal (shown once after server-side generation) ───────────────

function PrivateKeyModal({
  name,
  pem,
  onClose,
}: {
  name: string
  pem: string
  onClose: () => void
}) {
  const textRef = useRef<HTMLPreElement>(null)

  function copyPem() {
    navigator.clipboard.writeText(pem)
    toast({ title: 'Copied to clipboard' })
  }

  function downloadPem() {
    const blob = new Blob([pem], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}.pem`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Save your private key</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-sm text-amber-400 font-medium">
            ⚠ This is the only time the private key will be shown. Download or copy it now.
          </p>
          <pre
            ref={textRef}
            className="rounded-md border border-border bg-muted/60 p-3 text-xs font-mono overflow-auto max-h-64 whitespace-pre-wrap break-all"
          >
            {pem}
          </pre>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={copyPem}>
              <Copy className="h-4 w-4 mr-1" />
              Copy
            </Button>
            <Button size="sm" onClick={downloadPem}>
              <Download className="h-4 w-4 mr-1" />
              Download {name}.pem
            </Button>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
