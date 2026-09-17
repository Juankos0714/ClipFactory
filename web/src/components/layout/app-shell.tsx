import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  BarChart3,
  Clapperboard,
  Factory,
  LayoutDashboard,
  ListOrdered,
  Menu,
  RadioTower,
  Settings,
  Share2,
  Workflow,
  X,
} from 'lucide-react'
import { useAuth } from '@/providers/AuthProvider'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
  phase?: string
}

const NAV: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/channels', label: 'Channels', icon: RadioTower },
  { to: '/clips', label: 'Clips', icon: Clapperboard },
  { to: '/production', label: 'Production', icon: Factory },
  { to: '/production/queue', label: 'Queue', icon: ListOrdered },
  { to: '/automation', label: 'Automation', icon: Workflow, phase: 'FASE 5' },
  { to: '/publications', label: 'Publications', icon: Share2, phase: 'FASE 6' },
  { to: '/analytics', label: 'Analytics', icon: BarChart3, phase: 'FASE 6' },
  { to: '/settings', label: 'Settings', icon: Settings, phase: 'FASE 7' },
]

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Principal" className="flex flex-col gap-1 p-3">
      {NAV.map((item) => {
        const Icon = item.icon
        return (
          <NavLink
            key={item.to}
            to={item.to}
            onClick={onNavigate}
            className={({ isActive }) =>
              `flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
                isActive
                  ? 'bg-surface-elevated text-brand'
                  : 'text-neutral-300 hover:bg-surface-elevated hover:text-neutral-100'
              }`
            }
          >
            <Icon aria-hidden="true" className="h-4 w-4" />
            <span className="flex-1">{item.label}</span>
            {item.phase ? (
              <span
                className="rounded-full border border-border px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-neutral-500"
                title={`Función prevista para ${item.phase}`}
              >
                F{item.phase.slice(-1)}
              </span>
            ) : null}
          </NavLink>
        )
      })}
    </nav>
  )
}

const TITLES: Record<string, string> = {
  '/': 'Dashboard',
  '/channels': 'Channels',
  '/clips': 'Clips',
  '/production': 'Production',
  '/production/queue': 'Queue',
  '/automation': 'Automation',
  '/publications': 'Publications',
  '/analytics': 'Analytics',
  '/settings': 'Settings',
}

function ServerPill() {
  const { missingEndpoints } = useAuth()
  const blocked = missingEndpoints.length > 0
  return (
    <span
      role="status"
      className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium ${
        blocked
          ? 'border-amber-500/30 bg-amber-500/10 text-amber-400'
          : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400'
      }`}
    >
      <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${blocked ? 'bg-amber-400' : 'bg-emerald-400'}`} />
      {blocked
        ? missingEndpoints.length === 1
          ? '1 endpoint REQUIRED pendiente'
          : `${missingEndpoints.length} endpoints REQUIRED pendientes`
        : 'API conectada'}
    </span>
  )
}

export function AppShell() {
  const location = useLocation()
  const [drawerOpen, setDrawerOpen] = useState(false)

  useEffect(() => {
    setDrawerOpen(false)
  }, [location.pathname])

  const title = TITLES[location.pathname] ?? 'ClipFactory'

  return (
    <div className="min-h-svh bg-surface">
      <a
        href="#main"
        className="sr-only h-0 w-0 overflow-hidden focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-md focus:bg-brand focus:px-3 focus:py-2 focus:text-sm focus:text-white"
      >
        Saltar al contenido
      </a>

      {/* Sidebar fijo en desktop */}
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-60 border-r border-border bg-surface lg:block">
        <div className="flex h-16 items-center gap-2 border-b border-border px-4">
          <span className="text-lg" aria-hidden="true">
            🎬
          </span>
          <span className="font-semibold text-neutral-100">ClipFactory</span>
        </div>
        <NavList />
      </aside>

      {/* Drawer móvil */}
      {drawerOpen ? (
        <div className="fixed inset-0 z-40 lg:hidden" role="presentation">
          <div aria-hidden="true" onClick={() => setDrawerOpen(false)} className="absolute inset-0 bg-black/60" />
          <aside
            role="dialog"
            aria-modal="true"
            aria-label="Navegación"
            className="absolute inset-y-0 left-0 flex w-72 flex-col border-r border-border bg-surface"
          >
            <div className="flex h-16 items-center justify-between border-b border-border px-4">
              <span className="font-semibold text-neutral-100">ClipFactory</span>
              <button
                type="button"
                aria-label="Cerrar menú"
                onClick={() => setDrawerOpen(false)}
                className="rounded-md p-1 text-neutral-400 hover:bg-surface-elevated"
              >
                <X aria-hidden="true" className="h-5 w-5" />
              </button>
            </div>
            <NavList onNavigate={() => setDrawerOpen(false)} />
          </aside>
        </div>
      ) : null}

      <div className="flex min-h-svh flex-col lg:pl-60">
        <header className="sticky top-0 z-20 flex h-16 items-center justify-between gap-3 border-b border-border bg-surface/90 px-4 backdrop-blur">
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label="Abrir menú"
              onClick={() => setDrawerOpen(true)}
              className="rounded-md p-1.5 text-neutral-300 hover:bg-surface-elevated lg:hidden"
            >
              <Menu aria-hidden="true" className="h-5 w-5" />
            </button>
            <h1 className="text-base font-semibold text-neutral-100">{title}</h1>
          </div>
          <ServerPill />
        </header>

        <main id="main" className="flex-1 p-4 sm:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}