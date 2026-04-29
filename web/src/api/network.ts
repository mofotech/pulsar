import { apiGet, apiPost, apiPatch, apiPut, apiDelete, apiDeleteBody } from './client'
import type { Network, Subnet, Port, Router, FloatingIP, SecurityGroup, FirewallPolicy, CreateFirewallPolicyRequest, UpdateFirewallPolicyRequest } from '@/types/network'

// Networks
export const listNetworks = () => apiGet<Network[]>('/v1/network/networks')
export const getNetwork = (id: string) => apiGet<Network>(`/v1/network/networks/${id}`)
export const createNetwork = (req: Partial<Network>) =>
  apiPost<Network>('/v1/network/networks', req)
export const updateNetwork = (id: string, req: { name?: string; external?: boolean }) =>
  apiPatch<Network>(`/v1/network/networks/${id}`, req)
export const deleteNetwork = (id: string) => apiDelete(`/v1/network/networks/${id}`)

// Subnets
export const listSubnets = () => apiGet<Subnet[]>('/v1/network/subnets')
export const getSubnet = (id: string) => apiGet<Subnet>(`/v1/network/subnets/${id}`)
export const createSubnet = (req: Partial<Subnet>) =>
  apiPost<Subnet>('/v1/network/subnets', req)
export const deleteSubnet = (id: string) => apiDelete(`/v1/network/subnets/${id}`)

// Ports
export const listPorts = () => apiGet<Port[]>('/v1/network/ports')
export const getPort = (id: string) => apiGet<Port>(`/v1/network/ports/${id}`)
export const deletePort = (id: string) => apiDelete(`/v1/network/ports/${id}`)

// Routers
export const listRouters = () => apiGet<Router[]>('/v1/network/routers')
export const getRouter = (id: string) => apiGet<Router>(`/v1/network/routers/${id}`)
export const createRouter = (req: { name: string }) => apiPost<Router>('/v1/network/routers', req)
export const deleteRouter = (id: string) => apiDelete(`/v1/network/routers/${id}`)
export const setRouterGateway = (id: string, req: { external_network_id: string }) =>
  apiPut<Router>(`/v1/network/routers/${id}/gateway`, req)
export const clearRouterGateway = (id: string) =>
  apiDelete(`/v1/network/routers/${id}/gateway`)
export const addRouterInterface = (id: string, req: { subnet_id: string }) =>
  apiPut<Router>(`/v1/network/routers/${id}/interfaces`, req)
export const removeRouterInterface = (id: string, req: { subnet_id: string }) =>
  apiDeleteBody<Router>(`/v1/network/routers/${id}/interfaces`, req)

// Floating IPs
export const listFloatingIPs = () => apiGet<FloatingIP[]>('/v1/network/floatingips')
export const getFloatingIP = (id: string) => apiGet<FloatingIP>(`/v1/network/floatingips/${id}`)
export const createFloatingIP = () =>
  apiPost<FloatingIP>('/v1/network/floatingips')
export const updateFloatingIP = (id: string, req: { port_id: string }) =>
  apiPatch<FloatingIP>(`/v1/network/floatingips/${id}`, req)
export const deleteFloatingIP = (id: string) => apiDelete(`/v1/network/floatingips/${id}`)

// Security Groups
export const listSecurityGroups = () => apiGet<SecurityGroup[]>('/v1/network/security-groups')
export const getSecurityGroup = (id: string) =>
  apiGet<SecurityGroup>(`/v1/network/security-groups/${id}`)
export const createSecurityGroup = (req: { name: string }) =>
  apiPost<SecurityGroup>('/v1/network/security-groups', req)
export const deleteSecurityGroup = (id: string) =>
  apiDelete(`/v1/network/security-groups/${id}`)
export const addSecurityGroupRule = (
  sgId: string,
  req: {
    direction: string
    protocol?: string
    port_range_min?: number
    port_range_max?: number
    remote_ip_prefix?: string
    ethertype?: string
  },
) => apiPost(`/v1/network/security-groups/${sgId}/rules`, req)
export const deleteSecurityGroupRule = (sgId: string, ruleId: string) =>
  apiDelete(`/v1/network/security-groups/${sgId}/rules/${ruleId}`)

// Ports – security group assignment
export const updatePortSecurityGroups = (portId: string, sgIds: string[]) =>
  apiPatch<Port>(`/v1/network/ports/${portId}`, { security_group_ids: sgIds })

// FWaaS
export const listFirewallPolicies = () => apiGet<FirewallPolicy[]>('/v1/network/firewall-policies')
export const getFirewallPolicy = (id: string) => apiGet<FirewallPolicy>(`/v1/network/firewall-policies/${id}`)
export const createFirewallPolicy = (req: CreateFirewallPolicyRequest) =>
  apiPost<FirewallPolicy>('/v1/network/firewall-policies', req)
export const updateFirewallPolicy = (id: string, req: UpdateFirewallPolicyRequest) =>
  apiPatch<FirewallPolicy>(`/v1/network/firewall-policies/${id}`, req)
export const deleteFirewallPolicy = (id: string) =>
  apiDelete(`/v1/network/firewall-policies/${id}`)
