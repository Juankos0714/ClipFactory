import { Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/app-shell'
import { DashboardPage } from '@/features/dashboard/dashboard-page'
import { ChannelsPage } from '@/features/channels/channels-page'
import { ClipsPage } from '@/features/clips/clips-page'
import { ClipDetailPage } from '@/features/clips/clip-detail-page'
import { ProductionPage } from '@/features/production/production-page'
import { QueuePage } from '@/features/production/queue-page'
import { ComingSoon } from '@/features/placeholder/coming-soon'
import { NotFound } from '@/features/placeholder/not-found'

/** Sitemap: 8 secciones del maestro + rutas de detalle y 404. */
export default function App() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/channels" element={<ChannelsPage />} />
        <Route path="/clips" element={<ClipsPage />} />
        <Route path="/clips/:clipId" element={<ClipDetailPage />} />
        <Route path="/production" element={<ProductionPage />} />
        <Route path="/production/queue" element={<QueuePage />} />
        <Route path="/automation" element={<ComingSoon name="Automation" phase="FASE 5" />} />
        <Route path="/publications" element={<ComingSoon name="Publications" phase="FASE 6" />} />
        <Route path="/analytics" element={<ComingSoon name="Analytics" phase="FASE 6" />} />
        <Route path="/settings" element={<ComingSoon name="Settings" phase="FASE 7" />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}