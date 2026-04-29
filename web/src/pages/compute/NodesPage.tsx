import { useQuery } from '@tanstack/react-query'
import { PageHeader } from '@/components/common/PageHeader'
import { ResourceTable, type Column } from '@/components/common/ResourceTable'

import { listNodes } from '@/api/compute'
import type { Node } from '@/types/compute'

export default function NodesPage() {
  const { data: nodes = [], isLoading, error } = useQuery({
    queryKey: ['nodes'],
    queryFn: listNodes,
    refetchInterval: 15_000,
  })

  const columns: Column<Node>[] = [
    { key: 'agent_id', header: 'Agent ID' },
    { key: 'pillar', header: 'Pillar' },
    {
      key: 'hypervisor_types',
      header: 'Hypervisors',
      render: (r) => r.hypervisor_types?.join(', ') || '—',
    },
    {
      key: 'vcpus_used',
      header: 'vCPUs',
      render: (r) => `${r.vcpus_used} / ${r.vcpus_total}`,
    },
    {
      key: 'ram_mb_used',
      header: 'RAM',
      render: (r) => `${(r.ram_mb_used / 1024).toFixed(1)} / ${(r.ram_mb_total / 1024).toFixed(1)} GiB`,
    },
    {
      key: 'last_seen',
      header: 'Last Seen',
      render: (r) => new Date(r.last_seen).toLocaleString(),
    },
  ]

  return (
    <div>
      <PageHeader
        title="Nodes"
        description="Registered agent nodes"
      />
      <ResourceTable
        columns={columns}
        data={nodes as unknown as Record<string, unknown>[]}
        isLoading={isLoading}
        error={error}
        emptyMessage="No nodes registered."
      />
    </div>
  )
}
