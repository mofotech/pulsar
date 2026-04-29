import { apiGet, apiDelete } from './client'
import type { Image } from '@/types/images'

export const listImages = () => apiGet<Image[]>('/v1/images')
export const deleteImage = (id: string) => apiDelete(`/v1/images/${id}`)
