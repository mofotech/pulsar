import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'
import { Toaster } from '@/components/ui/toaster'

export function Shell() {
  return (
    <div className="flex min-h-screen bg-background">
      <Sidebar />
      <TopBar />
      <main className="ml-60 flex-1 min-h-screen pt-11">
        <div className="mx-auto max-w-7xl px-6 py-8">
          <Outlet />
        </div>
      </main>
      <Toaster />
    </div>
  )
}
