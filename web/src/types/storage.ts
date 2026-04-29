export interface QoSSpec {
  read_iops_sec?: number
  write_iops_sec?: number
  total_iops_sec?: number
  read_bytes_sec?: number
  write_bytes_sec?: number
  total_bytes_sec?: number
}

export interface Volume {
  id: string
  project_id: string
  name: string
  description?: string
  size_gb: number
  volume_type_id?: string
  status: string
  bootable?: boolean
  image_id?: string
  attached_to?: string
  attached_host?: string
  device_path?: string
  host_dev_path?: string
  iscsi_iqn?: string
  iscsi_portal?: string
  qos?: QoSSpec
  created_at: string
  updated_at: string
}

export interface Snapshot {
  id: string
  project_id: string
  volume_id: string
  name: string
  description?: string
  size_gb: number
  status: string
  created_at: string
  updated_at: string
}

export interface VolumeType {
  id: string
  name: string
  driver: string
  extra_specs?: Record<string, string>
  created_at: string
}

export interface VolumeActionRequest {
  action: 'attach' | 'detach' | 'extend' | 'set-qos'
  instance_id?: string
  device_path?: string
  new_size_gb?: number
  qos?: QoSSpec
}
