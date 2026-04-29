import { useEffect, useRef, useState } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { getConsole, instanceAction } from '@/api/compute'

// Use noVNC v1.4.0 — last version that ships proper ES modules in core/
// and has no top-level-await issues.
const NOVNC_CDN = 'https://cdn.jsdelivr.net/npm/@novnc/novnc@1.4.0/core/rfb.js'

let novncLoadPromise: Promise<unknown> | null = null

function loadNoVNC(): Promise<unknown> {
  if (window.RFB) return Promise.resolve(window.RFB)
  if (novncLoadPromise) return novncLoadPromise
  novncLoadPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.type = 'module'
    script.textContent = `
      import RFB from '${NOVNC_CDN}';
      window.RFB = RFB;
      window.dispatchEvent(new Event('novnc-ready'));
    `
    script.onerror = reject
    window.addEventListener('novnc-ready', () => resolve(window.RFB), { once: true })
    document.head.appendChild(script)
  })
  return novncLoadPromise
}

declare global {
  interface Window {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    RFB: any
  }
}

interface Props {
  instanceId: string
  instanceName: string
  open: boolean
  onClose: () => void
}

export function ConsoleModal({ instanceId, instanceName, open, onClose }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const rfbRef = useRef<any>(null)
  const [status, setStatus] = useState<'loading' | 'connecting' | 'connected' | 'error'>('loading')
  const [errorMsg, setErrorMsg] = useState('')
  const [actionPending, setActionPending] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return

    let cancelled = false

    async function connect() {
      setStatus('loading')
      setErrorMsg('')
      try {
        const [RFB, info] = await Promise.all([loadNoVNC(), getConsole(instanceId)])
        if (cancelled || !containerRef.current) return

        setStatus('connecting')
        const proto = location.protocol === 'https:' ? 'wss' : 'ws'
        const wsUrl = `${proto}://${location.host}${info.ws_path}`

        const rfb = new RFB(containerRef.current, wsUrl, { wsProtocols: ['binary'] })
        rfbRef.current = rfb
        rfb.scaleViewport = true

        rfb.addEventListener('connect', () => { if (!cancelled) setStatus('connected') })
        rfb.addEventListener('disconnect', (e: CustomEvent<{ clean: boolean }>) => {
          if (!cancelled && !e.detail.clean) {
            setStatus('error')
            setErrorMsg('Connection closed unexpectedly')
          }
        })
        rfb.addEventListener('credentialsrequired', () => rfb.sendCredentials({ password: '' }))
      } catch (e) {
        if (!cancelled) {
          setStatus('error')
          setErrorMsg(e instanceof Error ? e.message : String(e))
        }
      }
    }

    connect()

    return () => {
      cancelled = true
      if (rfbRef.current) {
        rfbRef.current.disconnect()
        rfbRef.current = null
      }
    }
  }, [open, instanceId])

  async function doAction(action: string) {
    setActionPending(action)
    try {
      await instanceAction(instanceId, action as never)
    } finally {
      setActionPending(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="max-w-5xl w-full p-0 gap-0 overflow-hidden">
        <DialogTitle className="sr-only">Console — {instanceName}</DialogTitle>
        <div className="flex items-center justify-between px-4 py-2 bg-muted border-b text-sm font-medium">
          <span>Console — {instanceName}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">
              {status === 'loading' && 'Loading noVNC…'}
              {status === 'connecting' && 'Connecting…'}
              {status === 'connected' && 'Connected'}
              {status === 'error' && `Error: ${errorMsg}`}
            </span>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              disabled={!!actionPending}
              onClick={() => doAction('stop')}
            >
              {actionPending === 'stop' ? 'Stopping…' : 'Stop'}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              disabled={!!actionPending}
              onClick={() => doAction('reboot')}
            >
              {actionPending === 'reboot' ? 'Rebooting…' : 'Reboot'}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              disabled={!!actionPending}
              onClick={() => doAction('hard-reboot')}
            >
              {actionPending === 'hard-reboot' ? 'Rebooting…' : 'Hard Reboot'}
            </Button>
          </div>
        </div>
        <div
          ref={containerRef}
          className="w-full bg-black"
          style={{ height: '600px' }}
        />
      </DialogContent>
    </Dialog>
  )
}
