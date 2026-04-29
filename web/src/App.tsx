import { Routes, Route, Navigate } from 'react-router-dom'
import { useAuthStore } from '@/store/auth'
import { Shell } from '@/components/layout/Shell'

import LoginPage from '@/pages/LoginPage'
import DashboardPage from '@/pages/DashboardPage'

import InstancesPage from '@/pages/compute/InstancesPage'
import InstanceDetailPage from '@/pages/compute/InstanceDetailPage'
import FlavorsPage from '@/pages/compute/FlavorsPage'
import NodesPage from '@/pages/compute/NodesPage'
import KeyPairsPage from '@/pages/compute/KeyPairsPage'
import LaunchInstancePage from '@/pages/compute/LaunchInstancePage'

import NetworksPage from '@/pages/network/NetworksPage'
import RoutersPage from '@/pages/network/RoutersPage'
import FloatingIPsPage from '@/pages/network/FloatingIPsPage'
import PortsPage from '@/pages/network/PortsPage'
import SecurityGroupsPage from '@/pages/network/SecurityGroupsPage'
import FirewallPoliciesPage from '@/pages/network/FirewallPoliciesPage'

import VolumesPage from '@/pages/storage/VolumesPage'
import SnapshotsPage from '@/pages/storage/SnapshotsPage'
import VolumeTypesPage from '@/pages/storage/VolumeTypesPage'

import ImagesPage from '@/pages/images/ImagesPage'

import ProjectsPage from '@/pages/identity/ProjectsPage'
import UsersPage from '@/pages/identity/UsersPage'
import OrgsPage from '@/pages/identity/OrgsPage'
import IdpPage from '@/pages/identity/IdpPage'
import AccessTokensPage from '@/pages/identity/AccessTokensPage'
import OIDCCallbackPage from '@/pages/OIDCCallbackPage'

function AuthGuard({ children }: { children: React.ReactNode }) {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  if (!isAuthenticated()) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/auth/callback" element={<OIDCCallbackPage />} />
      <Route path="/" element={<Navigate to="/compute/instances" replace />} />
      <Route
        element={
          <AuthGuard>
            <Shell />
          </AuthGuard>
        }
      >
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/compute/instances" element={<InstancesPage />} />
        <Route path="/compute/instances/launch" element={<LaunchInstancePage />} />
        <Route path="/compute/instances/:id" element={<InstanceDetailPage />} />
        <Route path="/compute/flavors" element={<FlavorsPage />} />
        <Route path="/compute/nodes" element={<NodesPage />} />
        <Route path="/compute/keypairs" element={<KeyPairsPage />} />
        <Route path="/network/networks" element={<NetworksPage />} />
        <Route path="/network/routers" element={<RoutersPage />} />
        <Route path="/network/floating-ips" element={<FloatingIPsPage />} />
        <Route path="/network/ports" element={<PortsPage />} />
        <Route path="/network/security-groups" element={<SecurityGroupsPage />} />
        <Route path="/network/firewall-policies" element={<FirewallPoliciesPage />} />
        <Route path="/storage/volumes" element={<VolumesPage />} />
        <Route path="/storage/snapshots" element={<SnapshotsPage />} />
        <Route path="/storage/volume-types" element={<VolumeTypesPage />} />
        <Route path="/images" element={<ImagesPage />} />
        <Route path="/identity/projects" element={<ProjectsPage />} />
        <Route path="/identity/users" element={<UsersPage />} />
        <Route path="/identity/orgs" element={<OrgsPage />} />
        <Route path="/identity/idps" element={<IdpPage />} />
        <Route path="/identity/access-tokens" element={<AccessTokensPage />} />
      </Route>
    </Routes>
  )
}
