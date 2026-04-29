import { useQuery } from '@tanstack/react-query'
import { Cpu, Network, HardDrive, Radio, TrendingUp } from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { PageHeader } from '@/components/common/PageHeader'
import { listInstances, listNodes } from '@/api/compute'
import { listNetworks } from '@/api/network'
import { listVolumes } from '@/api/storage'

interface StatCardProps {
  title: string
  value: string | number
  subValue?: string
  icon: React.ReactNode
  accent: string
  loading: boolean
}

function StatCard({ title, value, subValue, icon, accent, loading }: StatCardProps) {
  return (
    <Card className="relative overflow-hidden">
      {/* Accent line top */}
      <div className={`absolute inset-x-0 top-0 h-px ${accent}`} />
      <CardContent className="p-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {title}
            </p>
            {loading ? (
              <div className="mt-2 h-7 w-20 animate-pulse rounded-md bg-muted/60" />
            ) : (
              <p className="mt-1.5 text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
            )}
            {subValue && !loading && (
              <p className="mt-0.5 text-xs text-muted-foreground">{subValue}</p>
            )}
          </div>
          <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted/60 ${accent.replace('bg-gradient-to-r', '').split(' ')[0]}`}>
            <span className="text-muted-foreground">{icon}</span>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

export default function DashboardPage() {
  const instances = useQuery({ queryKey: ['instances'], queryFn: listInstances })
  const networks = useQuery({ queryKey: ['networks'], queryFn: listNetworks })
  const volumes = useQuery({ queryKey: ['volumes'], queryFn: listVolumes })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: listNodes })

  const activeInstances = instances.data?.filter((i) => i.status === 'active').length ?? 0
  const totalInstances = instances.data?.length ?? 0

  return (
    <div>
      <PageHeader
        title="Dashboard"
        description="Overview of your infrastructure"
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="Instances"
          value={`${activeInstances} / ${totalInstances}`}
          subValue={`${totalInstances - activeInstances} inactive`}
          icon={<Cpu className="h-4 w-4" />}
          accent="bg-gradient-to-r from-violet-500/60 to-violet-500/0"
          loading={instances.isLoading}
        />
        <StatCard
          title="Networks"
          value={networks.data?.length ?? 0}
          icon={<Network className="h-4 w-4" />}
          accent="bg-gradient-to-r from-blue-500/60 to-blue-500/0"
          loading={networks.isLoading}
        />
        <StatCard
          title="Volumes"
          value={volumes.data?.length ?? 0}
          icon={<HardDrive className="h-4 w-4" />}
          accent="bg-gradient-to-r from-emerald-500/60 to-emerald-500/0"
          loading={volumes.isLoading}
        />
        <StatCard
          title="Nodes"
          value={nodes.data?.length ?? 0}
          icon={<Radio className="h-4 w-4" />}
          accent="bg-gradient-to-r from-amber-500/60 to-amber-500/0"
          loading={nodes.isLoading}
        />
      </div>

      {(instances.error || networks.error || volumes.error || nodes.error) && (
        <div className="mt-6 flex items-start gap-2.5 rounded-xl bg-destructive/10 px-4 py-3 text-sm text-destructive ring-1 ring-destructive/20">
          <TrendingUp className="mt-0.5 h-4 w-4 shrink-0 rotate-180" />
          Some data failed to load. Check your connection and credentials.
        </div>
      )}
    </div>
  )
}
