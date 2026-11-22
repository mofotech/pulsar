/** Builds an unsigned JWT the way the controller encodes it (base64url, no padding). */
export function makeJwt(payload: Record<string, unknown>): string {
  const b64url = (s: string) =>
    btoa(String.fromCharCode(...new TextEncoder().encode(s)))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `${b64url('{"alg":"HS256","typ":"JWT"}')}.${b64url(JSON.stringify(payload))}.sig`
}

export const inOneHour = () => new Date(Date.now() + 3600_000).toISOString()
