import { apiGet, apiPost, apiDelete } from './client'
import type { Volume, Snapshot, VolumeType, VolumeActionRequest } from '@/types/storage'

// Volumes
export const listVolumes = () => apiGet<Volume[]>('/v1/storage/volumes')
export const getVolume = (id: string) => apiGet<Volume>(`/v1/storage/volumes/${id}`)
export const createVolume = (req: { name: string; size_gb: number; volume_type_id?: string; description?: string; image_id?: string }) =>
  apiPost<Volume>('/v1/storage/volumes', req)
export const deleteVolume = (id: string) => apiDelete(`/v1/storage/volumes/${id}`)
export const volumeAction = (id: string, req: VolumeActionRequest) =>
  apiPost<Volume>(`/v1/storage/volumes/${id}/action`, req)

// Snapshots
export const listSnapshots = () => apiGet<Snapshot[]>('/v1/storage/snapshots')
export const getSnapshot = (id: string) => apiGet<Snapshot>(`/v1/storage/snapshots/${id}`)
export const createSnapshot = (req: { volume_id: string; name: string; description?: string }) =>
  apiPost<Snapshot>('/v1/storage/snapshots', req)
export const deleteSnapshot = (id: string) => apiDelete(`/v1/storage/snapshots/${id}`)

// Volume Types
export const listVolumeTypes = () => apiGet<VolumeType[]>('/v1/storage/volume-types')
export const getVolumeType = (id: string) => apiGet<VolumeType>(`/v1/storage/volume-types/${id}`)
export const createVolumeType = (req: { name: string; driver: string }) =>
  apiPost<VolumeType>('/v1/storage/volume-types', req)
export const deleteVolumeType = (id: string) => apiDelete(`/v1/storage/volume-types/${id}`)
