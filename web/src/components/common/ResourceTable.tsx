import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

export interface Column<T> {
  key: string
  header: string
  render?: (row: T) => React.ReactNode
}

interface ResourceTableProps<T> {
  columns: Column<T>[]
  data: T[]
  isLoading?: boolean
  error?: Error | null
  onRowClick?: (row: T) => void
  emptyMessage?: string
}

function SkeletonRow({ cols }: { cols: number }) {
  return (
    <TableRow className="border-border/50">
      {Array.from({ length: cols }).map((_, i) => (
        <TableCell key={i}>
          <div className="h-4 w-full animate-pulse rounded-full bg-muted/60" style={{ width: `${50 + Math.random() * 40}%` }} />
        </TableCell>
      ))}
    </TableRow>
  )
}

export function ResourceTable<T>({
  columns,
  data,
  isLoading,
  error,
  onRowClick,
  emptyMessage = 'No items found.',
}: ResourceTableProps<T>) {
  const rows = data ?? []
  return (
    <div className="rounded-xl border border-border/60 bg-card shadow-card overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow className="border-border/60 hover:bg-transparent">
            {columns.map((col) => (
              <TableHead
                key={col.key}
                className="h-10 bg-muted/30 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground"
              >
                {col.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <SkeletonRow key={i} cols={columns.length} />
            ))
          ) : error ? (
            <TableRow>
              <TableCell
                colSpan={columns.length}
                className="py-12 text-center text-sm text-destructive"
              >
                {error.message}
              </TableCell>
            </TableRow>
          ) : rows.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={columns.length}
                className="py-12 text-center text-sm text-muted-foreground"
              >
                {emptyMessage}
              </TableCell>
            </TableRow>
          ) : (
            rows.map((row, idx) => (
              <TableRow
                key={idx}
                onClick={() => onRowClick?.(row)}
                className={cn(
                  'border-border/40 transition-colors',
                  onRowClick && 'cursor-pointer hover:bg-primary/[0.04]'
                )}
              >
                {columns.map((col) => (
                  <TableCell key={col.key} className="text-sm">
                    {col.render ? col.render(row) : String((row as Record<string, unknown>)[col.key] ?? '')}
                  </TableCell>
                ))}
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </div>
  )
}
