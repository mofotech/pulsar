import { cn } from '@/lib/utils'

interface StatusBadgeProps {
  status: string
  className?: string
}

interface StatusStyle {
  dot: string
  text: string
  bg: string
}

function getStatusStyle(status: string): StatusStyle {
  const s = (status ?? '').toLowerCase()

  if (['active', 'available', 'up'].includes(s)) {
    return { dot: 'bg-emerald-400', text: 'text-emerald-400', bg: 'bg-emerald-400/10 ring-emerald-400/20' }
  }
  if (['building', 'creating', 'scheduling', 'scheduled', 'starting', 'queued', 'build'].includes(s)) {
    return { dot: 'bg-amber-400 animate-pulse', text: 'text-amber-400', bg: 'bg-amber-400/10 ring-amber-400/20' }
  }
  if (['stopping', 'deleting', 'down'].includes(s)) {
    return { dot: 'bg-orange-400', text: 'text-orange-400', bg: 'bg-orange-400/10 ring-orange-400/20' }
  }
  if (s === 'error') {
    return { dot: 'bg-red-400', text: 'text-red-400', bg: 'bg-red-400/10 ring-red-400/20' }
  }
  if (s === 'unknown') {
    return { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-400', bg: 'bg-yellow-400/10 ring-yellow-400/20' }
  }
  if (['stopped', 'deleted'].includes(s)) {
    return { dot: 'bg-zinc-500', text: 'text-zinc-400', bg: 'bg-zinc-500/10 ring-zinc-500/20' }
  }
  if (s === 'pending') {
    return { dot: 'bg-blue-400 animate-pulse', text: 'text-blue-400', bg: 'bg-blue-400/10 ring-blue-400/20' }
  }
  return { dot: 'bg-zinc-500', text: 'text-zinc-400', bg: 'bg-zinc-500/10 ring-zinc-500/20' }
}

export function StatusBadge({ status, className }: StatusBadgeProps) {
  if (!status) return <span className="text-muted-foreground text-xs">—</span>
  const style = getStatusStyle(status)
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1',
        style.bg,
        style.text,
        className
      )}
    >
      <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', style.dot)} />
      {status}
    </span>
  )
}
