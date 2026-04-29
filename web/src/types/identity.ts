export interface TokenResponse {
  token: string
  expires_at: string
}

export interface PersonalAccessToken {
  id: string
  name: string
  last_used_at: string | null
  expires_at: string | null
  created_at: string
}

export interface CreatePATResponse extends PersonalAccessToken {
  token: string // raw value, shown once only
}

export interface Organization {
  id: string
  name: string
  slug: string
  description: string
  status: string
  is_default: boolean
  created_at: string
}

export interface IdentityProvider {
  id: string
  org_id: string
  name: string
  slug: string
  provider_type: string
  issuer_url: string
  client_id: string
  scopes: string
  claim_email: string
  claim_name: string
  auto_provision: boolean
  default_role: string
  domain_hint: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface Project {
  id: string
  name: string
  status: string
  created_at: string
}

export interface User {
  id: string
  email: string
  role: string
  project_id: string
  created_at: string
}
