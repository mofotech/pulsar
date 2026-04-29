import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface ProjectState {
  /** The currently active project id (may differ from the JWT's project) */
  activeProjectId: string | null
  activeProjectName: string | null
  setActiveProject: (id: string, name: string) => void
  clearActiveProject: () => void
}

export const useProjectStore = create<ProjectState>()(
  persist(
    (set) => ({
      activeProjectId: null,
      activeProjectName: null,
      setActiveProject: (id, name) => set({ activeProjectId: id, activeProjectName: name }),
      clearActiveProject: () => set({ activeProjectId: null, activeProjectName: null }),
    }),
    { name: 'pulsar-project' }
  )
)
