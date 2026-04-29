export type InstanceStatus =
  | 'pending'
  | 'scheduling'
  | 'scheduled'
  | 'building'
  | 'active'
  | 'stopping'
  | 'stopped'
  | 'starting'
  | 'deleting'
  | 'deleted'
  | 'error'
  | 'unknown'

export interface Instance {
  id: string
  project_id: string
  name: string
  flavor_id: string
  image_id: string
  hypervisor_type: string
  status: InstanceStatus
  node_id?: string
  user_data?: string
  key_names?: string[]
  metadata?: Record<string, string>
  port_ids?: string[]
  boot_volume_id?: string
  delete_boot_volume?: boolean
  created_at: string
  updated_at: string
}

export interface Flavor {
  id: string
  name: string
  vcpus: number
  ram_mb: number
  disk_gb: number
  ephemeral_gb: number
  public: boolean
}

export interface NetworkAttachment {
  network_id: string
  port_id?: string
}

export interface BlockDeviceMapping {
  volume_id: string
  delete_on_terminate?: boolean
}

export interface CreateInstanceRequest {
  name: string
  flavor_id: string
  image_id?: string
  hypervisor_type?: string
  networks: NetworkAttachment[]
  user_data?: string
  key_names?: string[]
  metadata?: Record<string, string>
  block_device_mappings?: BlockDeviceMapping[]
}

export type InstanceAction = 'start' | 'stop' | 'reboot' | 'hard-reboot' | 'console'

export interface InstanceActionRequest {
  action: InstanceAction
}

export interface ResizeInstanceRequest {
  flavor_id: string
}

export interface ConsoleInfo {
  token: string
  ws_path: string
}

export interface Node {
  agent_id: string
  pillar: string
  hypervisor_types: string[]
  vcpus_total: number
  vcpus_used: number
  ram_mb_total: number
  ram_mb_used: number
  last_seen: string
}

export interface KeyPair {
  id: string
  user_id: string
  name: string
  public_key: string
  fingerprint: string
  created_at: string
}

export interface CreateKeyPairRequest {
  name: string
  public_key?: string // omit to have the server generate a key
}

export interface CreateKeyPairResponse {
  keypair: KeyPair
  private_key?: string // PEM, returned only once when server-generated
}
