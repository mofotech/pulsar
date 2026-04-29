import { useQuery, useQueryClient } from '@tanstack/react-query'
import { FolderOpen } from 'lucide-react'
import { useEffect } from 'react'
import { listProjects } from '@/api/identity'
import { useAuthStore } from '@/store/auth'
import { useProjectStore } from '@/store/project'
import type { Project } from '@/types/identity'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export function TopBar() {
  const { jwtProjectId } = useAuthStore()
  const isAdmin = useAuthStore((s) => s.isAdmin)()
  const { activeProjectId, setActiveProject } = useProjectStore()
  const qc = useQueryClient()

  const { data: projects = [] } = useQuery({
    queryKey: ['projects'],
    queryFn: () => listProjects(),
  })

  // Whenever the project list loads (or changes), ensure the active project is
  // one the current user can actually see. This handles:
  //   - First load with no active project set
  //   - Stale localStorage project from a previous session / different user
  //   - Switching users (org_admin logs in after platform_admin)
  useEffect(() => {
    if (projects.length === 0) return
    const isVisible = projects.find((p: Project) => p.id === activeProjectId)
    if (!isVisible) {
      // Prefer the project embedded in the JWT; fall back to first in list.
      const match = projects.find((p: Project) => p.id === jwtProjectId) ?? projects[0]
      setActiveProject(match.id, match.name)
    }
  }, [projects, jwtProjectId, activeProjectId, setActiveProject])

  function handleSwitch(projectId: string) {
    const project = projects.find((p: Project) => p.id === projectId)
    if (!project) return
    setActiveProject(project.id, project.name)
    // Invalidate all resource queries so they reload scoped to the new project
    qc.invalidateQueries()
  }

  const activeProject = projects.find((p: Project) => p.id === activeProjectId)

  return (
    <div
      className="fixed top-0 left-60 right-0 z-30 flex h-11 items-center gap-3 border-b border-white/[0.06] px-6"
      style={{ background: 'hsl(var(--sidebar-bg))' }}
    >
      <FolderOpen className="h-3.5 w-3.5 shrink-0 text-primary" />

      {isAdmin ? (
        // Admins get a full project switcher
        <Select value={activeProjectId ?? ''} onValueChange={handleSwitch}>
          <SelectTrigger className="h-7 w-56 border-white/[0.08] bg-white/[0.04] text-xs">
            <SelectValue placeholder="Select project…" />
          </SelectTrigger>
          <SelectContent>
            {projects.map((p: Project) => (
              <SelectItem key={p.id} value={p.id} className="text-xs">
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        // Members see only their own project name — no switcher
        <span className="text-xs font-medium text-foreground/80">
          {activeProject?.name ?? '…'}
        </span>
      )}

      {/* Right side — breadcrumbs / actions can go here later */}
      <div className="flex-1" />
    </div>
  )
}
