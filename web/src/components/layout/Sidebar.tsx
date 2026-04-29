import { NavLink, useNavigate } from 'react-router-dom'
import {
  Server,
  Network,
  Database,
  Image,
  Users,
  Zap,
  Cpu,
  Globe,
  HardDrive,
  Shield,
  ShieldAlert,
  Layers,
  GitBranch as _GitBranch, // kept for potential future use
  Radio,
  Box,
  Camera,
  FolderOpen,
  UserCog,
  Building2,
  LogOut,
  LayoutDashboard,
  KeyRound,
  KeySquare,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth'

interface NavItemProps {
  to: string
  icon: React.ReactNode
  label: string
}

function NavItem({ to, icon, label }: NavItemProps) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        cn(
          'group flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-all duration-150',
          isActive
            ? 'bg-primary/15 text-primary font-medium shadow-glow-sm'
            : 'text-muted-foreground hover:bg-white/[0.04] hover:text-foreground'
        )
      }
    >
      {({ isActive }) => (
        <>
          <span className={cn(
            'h-4 w-4 shrink-0 transition-colors',
            isActive ? 'text-primary' : 'text-muted-foreground group-hover:text-foreground'
          )}>
            {icon}
          </span>
          {label}
          {isActive && (
            <span className="ml-auto h-1.5 w-1.5 rounded-full bg-primary" />
          )}
        </>
      )}
    </NavLink>
  )
}

interface SectionProps {
  title: string
  icon: React.ReactNode
  children: React.ReactNode
}

function Section({ title, icon, children }: SectionProps) {
  return (
    <div className="mb-5">
      <div className="flex items-center gap-2 px-3 pb-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
        <span className="h-3 w-3 shrink-0">{icon}</span>
        {title}
      </div>
      <div className="space-y-0.5">{children}</div>
    </div>
  )
}

export function Sidebar() {
  const { email, logout } = useAuthStore()
  const isPlatformAdmin = useAuthStore((s) => s.isPlatformAdmin)()
  const isOrgAdmin = useAuthStore((s) => s.isOrgAdmin)()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <aside className="fixed inset-y-0 left-0 z-40 flex w-60 flex-col" style={{ background: 'hsl(var(--sidebar-bg))' }}>
      {/* Subtle right border */}
      <div className="absolute inset-y-0 right-0 w-px bg-gradient-to-b from-transparent via-white/[0.06] to-transparent" />

      {/* Brand */}
      <div className="flex h-14 items-center gap-3 px-4" style={{ borderBottom: '1px solid hsl(var(--sidebar-border))' }}>
        <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-primary/20 ring-1 ring-primary/30">
          <Zap className="h-3.5 w-3.5 text-primary" />
        </div>
        <div>
          <span className="text-sm font-semibold tracking-tight text-foreground">Pulsar</span>
          <span className="ml-1.5 rounded bg-primary/20 px-1 py-0.5 text-[9px] font-bold uppercase tracking-wider text-primary">Console</span>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto px-3 py-4">
        {/* Dashboard shortcut */}
        <div className="mb-4">
          <NavItem to="/" icon={<LayoutDashboard size={16} />} label="Dashboard" />
        </div>

        <Section title="Compute" icon={<Server size={12} />}>
          <NavItem to="/compute/instances" icon={<Cpu size={15} />} label="Instances" />
          <NavItem to="/compute/flavors" icon={<Layers size={15} />} label="Flavors" />
          <NavItem to="/compute/keypairs" icon={<KeyRound size={15} />} label="Key Pairs" />
          <NavItem to="/compute/nodes" icon={<Radio size={15} />} label="Nodes" />
        </Section>

        <Section title="Network" icon={<Network size={12} />}>
          <NavItem to="/network/networks" icon={<Globe size={15} />} label="Networks" />
          <NavItem to="/network/routers" icon={<Box size={15} />} label="Routers" />
          <NavItem to="/network/floating-ips" icon={<Radio size={15} />} label="Floating IPs" />
          <NavItem to="/network/ports" icon={<Network size={15} />} label="Ports" />
          <NavItem to="/network/security-groups" icon={<Shield size={15} />} label="Security Groups" />
          <NavItem to="/network/firewall-policies" icon={<ShieldAlert size={15} />} label="Firewall Policies" />
        </Section>

        <Section title="Storage" icon={<Database size={12} />}>
          <NavItem to="/storage/volumes" icon={<HardDrive size={15} />} label="Volumes" />
          <NavItem to="/storage/snapshots" icon={<Camera size={15} />} label="Snapshots" />
          <NavItem to="/storage/volume-types" icon={<Database size={15} />} label="Volume Types" />
        </Section>

        <Section title="Images" icon={<Image size={12} />}>
          <NavItem to="/images" icon={<Image size={15} />} label="Images" />
        </Section>

        <Section title="Identity" icon={<Users size={12} />}>
          {isPlatformAdmin && (
            <NavItem to="/identity/orgs" icon={<Building2 size={15} />} label="Organizations" />
          )}
          {isOrgAdmin && (
            <NavItem to="/identity/idps" icon={<KeyRound size={15} />} label="Identity Providers" />
          )}
          <NavItem to="/identity/projects" icon={<FolderOpen size={15} />} label="Projects" />
          <NavItem to="/identity/users" icon={<UserCog size={15} />} label="Users" />
          <NavItem to="/identity/access-tokens" icon={<KeySquare size={15} />} label="Access Tokens" />
        </Section>
      </nav>

      {/* User / Logout */}
      <div className="px-3 py-3" style={{ borderTop: '1px solid hsl(var(--sidebar-border))' }}>
        <button
          onClick={handleLogout}
          className="group flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm text-muted-foreground transition-all hover:bg-white/[0.04] hover:text-foreground"
        >
          <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/15 text-[10px] font-bold text-primary">
            {(email ?? 'U')[0].toUpperCase()}
          </div>
          <span className="min-w-0 flex-1 truncate text-left text-xs">{email ?? 'Logout'}</span>
          <LogOut size={14} className="shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />
        </button>
      </div>
    </aside>
  )
}
