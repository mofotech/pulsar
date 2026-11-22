/**
 * Decodes a JWT's payload WITHOUT verifying it (the server does that). Used
 * only to read display/authorization hints such as role and email.
 *
 * JWTs are base64url-encoded (RFC 7515: '-' and '_', no padding) and may carry
 * UTF-8; atob() alone rejects the former and mangles the latter.
 * Returns {} for anything malformed.
 */
export function decodeJwtPayload<T extends object = Record<string, unknown>>(token: string): Partial<T> {
  try {
    const part = token.split('.')[1]
    if (!part) return {}
    const b64 = part.replace(/-/g, '+').replace(/_/g, '/')
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4)
    const bytes = Uint8Array.from(atob(padded), (c) => c.charCodeAt(0))
    const json = JSON.parse(new TextDecoder().decode(bytes))
    return json && typeof json === 'object' ? (json as Partial<T>) : {}
  } catch {
    return {}
  }
}
