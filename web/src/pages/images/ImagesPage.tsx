import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'
import { StatusBadge } from '@/components/common/StatusBadge'
import { ConfirmDialog } from '@/components/common/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { toast } from '@/hooks/use-toast'
import { listImages, deleteImage } from '@/api/images'
import type { Image } from '@/types/images'

function formatBytes(bytes?: number): string {
  if (bytes == null) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

export default function ImagesPage() {
  const qc = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<Image | null>(null)

  const { data: images = [], isLoading, error } = useQuery({
    queryKey: ['images'],
    queryFn: listImages,
  })

  const deleteMutation = useMutation({
    mutationFn: deleteImage,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['images'] })
      setDeleteTarget(null)
      toast({ title: 'Image deleted' })
    },
    onError: (err: Error) => {
      toast({ variant: 'destructive', title: 'Delete failed', description: err.message })
    },
  })

  const columns: Column<Image>[] = [
    { key: 'name', header: 'Name' },
    { key: 'format', header: 'Format' },
    { key: 'status', header: 'Status', render: (r) => <StatusBadge status={r.status} /> },
    { key: 'size_bytes', header: 'Size', render: (r) => formatBytes(r.size_bytes) },
    {
      key: 'created_at',
      header: 'Created',
      render: (r) => new Date(r.created_at).toLocaleString(),
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
      <PageHeader title="Images" description="VM disk images" />
      <ResourceTable
        columns={columns}
        data={images}
        isLoading={isLoading}
        error={error}
        emptyMessage="No images."
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete Image"
        description={`Delete image "${deleteTarget?.name}"? This cannot be undone.`}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        loading={deleteMutation.isPending}
        destructive
      />
    </div>
  )
}
