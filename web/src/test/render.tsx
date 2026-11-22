import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

/** Renders the current location so tests can assert on navigation. */
function LocationProbe() {
  const loc = useLocation()
  return <div data-testid="location">{loc.pathname + loc.search}</div>
}

/**
 * Renders ui at `route` inside a router + fresh QueryClient (no retries, so
 * failures surface immediately). Any path other than `path` renders a stub
 * page, which makes redirects observable via the "location" test id.
 */
export function renderAt(ui: ReactElement, { route = '/', path = '*' } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[route]}>
        <LocationProbe />
        <Routes>
          <Route path={path} element={ui} />
          <Route path="*" element={<div>other page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}
