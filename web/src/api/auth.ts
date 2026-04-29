import { apiPost, apiDelete } from './client'
import type { TokenResponse } from '@/types/identity'

export const createToken = (email: string, password: string) =>
  apiPost<TokenResponse>('/v1/auth/tokens', { email, password })

export const deleteToken = () => apiDelete('/v1/auth/tokens')
