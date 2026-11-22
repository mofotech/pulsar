import '@testing-library/jest-dom/vitest'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { cleanup } from '@testing-library/react'
import { server } from './server'
import { useAuthStore } from '@/store/auth'
import { useProjectStore } from '@/store/project'

// Any request without a handler fails the test instead of silently hitting
// the network, so every API interaction a test relies on is explicit.
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
  localStorage.clear()
  useAuthStore.setState({
    token: null, expiresAt: null, email: null,
    jwtProjectId: null, role: null, orgRole: null, orgId: null,
  })
  useProjectStore.setState({ activeProjectId: null, activeProjectName: null })
})
afterAll(() => server.close())
