import { apiGet, apiPost, apiPatch, apiPut, apiDelete } from './client'
import type { IdentityProvider, Organization, PersonalAccessToken, CreatePATResponse, Project, User } from '@/types/identity'

export const listOrgs = () => apiGet<Organization[]>('/v1/orgs')
export const createOrg = (req: { name: string; slug: string; description?: string }) =>
  apiPost<Organization>('/v1/orgs', req)
export const updateOrg = (id: string, req: { name?: string; description?: string; status?: string }) =>
  apiPatch<Organization>(`/v1/orgs/${id}`, req)
export const deleteOrg = (id: string) => apiDelete(`/v1/orgs/${id}`)

export const listIdps = (orgId: string) => apiGet<IdentityProvider[]>(`/v1/orgs/${orgId}/idps`)

/** Public — no auth token needed. Returns enabled IDPs for the org the email belongs to. */
export const lookupIdps = (email: string) =>
  apiGet<IdentityProvider[]>(`/v1/auth/idps?email=${encodeURIComponent(email)}`)
export const createIdp = (orgId: string, req: {
  name: string
  issuer_url: string
  client_id: string
  client_secret: string
  scopes?: string
  claim_email?: string
  claim_name?: string
  auto_provision?: boolean
  default_role?: string
  domain_hint?: string
}) => apiPost<IdentityProvider>(`/v1/orgs/${orgId}/idps`, req)
export const updateIdp = (orgId: string, idpId: string, req: {
  name?: string
  issuer_url?: string
  client_id?: string
  client_secret?: string
  scopes?: string
  claim_email?: string
  claim_name?: string
  auto_provision?: boolean
  default_role?: string
  domain_hint?: string
  enabled?: boolean
}) => apiPatch<IdentityProvider>(`/v1/orgs/${orgId}/idps/${idpId}`, req)
export const deleteIdp = (orgId: string, idpId: string) =>
  apiDelete(`/v1/orgs/${orgId}/idps/${idpId}`)

export const listProjects = (orgId?: string) => {
  const qs = orgId ? `?org_id=${encodeURIComponent(orgId)}` : ''
  return apiGet<Project[]>(`/v1/projects${qs}`)
}
export const createProject = (name: string) => apiPost<Project>('/v1/projects', { name })
export const updateProject = (id: string, name: string) => apiPatch<Project>(`/v1/projects/${id}`, { name })
export const deleteProject = (id: string) => apiDelete(`/v1/projects/${id}`)

export const listUsers = () => apiGet<User[]>('/v1/users')
export const createUser = (req: { email: string; password: string; role: string; org_role?: string; project_id: string }) =>
  apiPost<User>('/v1/users', req)
export const updateUser = (id: string, req: { role?: string; password?: string }) =>
  apiPatch<User>(`/v1/users/${id}`, req)
export const deleteUser = (id: string) => apiDelete(`/v1/users/${id}`)

export const listUserProjects = (userId: string) => apiGet<Project[]>(`/v1/users/${userId}/projects`)
export const grantProjectAccess = (userId: string, projectId: string) => apiPut(`/v1/users/${userId}/projects/${projectId}`)
export const revokeProjectAccess = (userId: string, projectId: string) => apiDelete(`/v1/users/${userId}/projects/${projectId}`)

// ─── Personal Access Tokens ───────────────────────────────────────────────────

export const listPATs = () => apiGet<PersonalAccessToken[]>('/v1/auth/tokens/personal')
export const createPAT = (req: { name: string; expires_at?: string | null }) =>
  apiPost<CreatePATResponse>('/v1/auth/tokens/personal', req)
export const deletePAT = (id: string) => apiDelete(`/v1/auth/tokens/personal/${id}`)
