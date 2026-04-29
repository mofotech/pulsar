export interface Network {
  id: string
  project_id: string
  name: string
  type: string
  vni?: number
  vlan_id?: number
  external: boolean
  status: string
  created_at: string
}

export interface AllocationPool {
  start: string
  end: string
}

export interface Subnet {
  id: string
  project_id: string
  network_id: string
  name?: string
  cidr: string
  gateway_ip?: string
  allocation_pool?: AllocationPool
  dns_nameservers?: string[]
  created_at: string
}

export interface FixedIP {
  subnet_id: string
  ip_address: string
}

export interface Port {
  id: string
  project_id: string
  network_id: string
  subnet_id?: string
  name?: string
  mac_address: string
  fixed_ips: FixedIP[]
  security_group_ids?: string[]
  status: string
  device_id?: string
  created_at: string
}

export interface Router {
  id: string
  project_id: string
  name: string
  status: string
  external_network_id?: string
  external_ip?: string
  interface_subnets?: string[]
  created_at: string
}

export interface FloatingIP {
  id: string
  project_id: string
  floating_network_id: string
  floating_ip_address: string
  fixed_ip_address?: string
  port_id?: string
  router_id?: string
  status: string
}

export interface SecurityGroupRule {
  id: string
  direction: string
  protocol?: string
  port_range_min?: number
  port_range_max?: number
  remote_ip_prefix?: string
  remote_group_id?: string
  ethertype: string
}

export interface SecurityGroup {
  id: string
  project_id: string
  name: string
  description?: string
  rules?: SecurityGroupRule[]
}

export interface FirewallRule {
  id?: string
  direction: 'ingress' | 'egress'
  protocol?: string
  port_min?: number
  port_max?: number
  src_cidr?: string
  dst_cidr?: string
  action: 'allow' | 'drop'
  priority: number
}

export interface FirewallPolicy {
  id: string
  project_id: string
  router_id: string
  name: string
  rules: FirewallRule[]
  created_at: string
  updated_at: string
}

export interface CreateFirewallPolicyRequest {
  name: string
  router_id: string
  rules?: FirewallRule[]
}

export interface UpdateFirewallPolicyRequest {
  name?: string
  rules?: FirewallRule[]
}
