import { apiGet, apiPost, apiDelete, apiPut } from './client'
import type { Instance, Flavor, CreateInstanceRequest, InstanceAction, ResizeInstanceRequest, Node, ConsoleInfo, KeyPair, CreateKeyPairRequest, CreateKeyPairResponse } from '@/types/compute'
import type { Port } from '@/types/network'

export const listInstances = () => apiGet<Instance[]>('/v1/compute/instances')
export const getInstance = (id: string) => apiGet<Instance>(`/v1/compute/instances/${id}`)
export const createInstance = (req: CreateInstanceRequest) =>
  apiPost<Instance>('/v1/compute/instances', req)
export const deleteInstance = (id: string) => apiDelete(`/v1/compute/instances/${id}`)
export const instanceAction = (id: string, action: InstanceAction) =>
  apiPost<void>(`/v1/compute/instances/${id}/action`, { action })
export const resetInstance = (id: string) =>
  apiPost<Instance>(`/v1/compute/instances/${id}/reset`, {})

export const listFlavors = () => apiGet<Flavor[]>('/v1/compute/flavors')
export const getFlavor = (id: string) => apiGet<Flavor>(`/v1/compute/flavors/${id}`)
export const createFlavor = (req: Omit<Flavor, 'id'>) =>
  apiPost<Flavor>('/v1/compute/flavors', req)
export const deleteFlavor = (id: string) => apiDelete(`/v1/compute/flavors/${id}`)

export const listNodes = () => apiGet<Node[]>('/v1/compute/nodes')

export const getConsole = (id: string) =>
  apiGet<ConsoleInfo>(`/v1/compute/instances/${id}/console`)

export const attachInterface = (instanceId: string, req: { network_id?: string; port_id?: string }) =>
  apiPost<Port>(`/v1/compute/instances/${instanceId}/interfaces`, req)
export const detachInterface = (instanceId: string, portId: string) =>
  apiDelete(`/v1/compute/instances/${instanceId}/interfaces/${portId}`)

export const resizeInstance = (instanceId: string, req: ResizeInstanceRequest) =>
  apiPost<Instance>(`/v1/compute/instances/${instanceId}/resize`, req)

export const updateMetadata = (instanceId: string, metadata: Record<string, string>) =>
  apiPut<Instance>(`/v1/compute/instances/${instanceId}/metadata`, metadata)
export const setMetadataKey = (instanceId: string, key: string, value: string) =>
  apiPut<Instance>(`/v1/compute/instances/${instanceId}/metadata/${encodeURIComponent(key)}`, { value })
export const deleteMetadataKey = (instanceId: string, key: string) =>
  apiDelete(`/v1/compute/instances/${instanceId}/metadata/${encodeURIComponent(key)}`)

// ── Keypairs ──────────────────────────────────────────────────────────────────
export const listKeypairs = () => apiGet<KeyPair[]>('/v1/compute/keypairs')
export const getKeypair = (id: string) => apiGet<KeyPair>(`/v1/compute/keypairs/${id}`)
export const createKeypair = (req: CreateKeyPairRequest) =>
  apiPost<CreateKeyPairResponse>('/v1/compute/keypairs', req)
export const deleteKeypair = (id: string) => apiDelete(`/v1/compute/keypairs/${id}`)
