import { useState } from 'react'
import { Pencil, Plus, Power, RefreshCw, Trash2 } from 'lucide-react'
import { useSources, useCreateSource, useUpdateSource, useDeleteSource, useTriggerDiscovery } from '@/hooks/use-channels'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { formatDateTime } from '@/lib/format'
import { ChannelFormDialog, type ChannelFormValues } from './components/channel-form-dialog'
import type { Source } from '@/types/api'

export function ChannelsPage() {
  const { data, isLoading, error, refetch } = useSources()
  const createSource = useCreateSource()
  const updateSource = useUpdateSource()
  const deleteSource = useDeleteSource()
  const triggerDiscovery = useTriggerDiscovery()

  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Source | null>(null)
  const [deleting, setDeleting] = useState<Source | null>(null)
  const [syncingId, setSyncingId] = useState<number | null>(null)

  const sources = data?.data ?? []

  const handleSync = async (source: Source) => {
    setSyncingId(source.id)
    try {
      await triggerDiscovery.mutateAsync(source.id)
    } finally {
      setSyncingId(null)
    }
  }

  const handleSubmit = async (values: ChannelFormValues) => {
    if (editing) await updateSource.mutateAsync({ id: editing.id, input: { ...values } })
    else await createSource.mutateAsync(values)
    setFormOpen(false)
    setEditing(null)
  }

  const handleToggle = async (source: Source) => {
    await updateSource.mutateAsync({ id: source.id, input: { active: !source.active } })
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-neutral-400">
          {sources.length > 0 ? `${sources.length} canal(es) configurado(s)` : 'Configurá tu primer canal de contenido.'}
        </p>
        <Button
          onClick={() => {
            setEditing(null)
            setFormOpen(true)
          }}
        >
          <Plus aria-hidden="true" className="h-4 w-4" />
          Agregar canal
        </Button>
      </div>

      <Card>
        <StateView
          isLoading={isLoading}
          error={error}
          onRetry={() => void refetch()}
          isEmpty={sources.length === 0}
          emptyTitle="Sin canales"
          emptyBody="Agregá un canal de Twitch o Kick para que el discovery empiece a detectar clips."
          emptyAction={
            <Button
              size="sm"
              onClick={() => {
                setEditing(null)
                setFormOpen(true)
              }}
            >
              <Plus aria-hidden="true" className="h-4 w-4" />
              Agregar canal
            </Button>
          }
          loadingRows={4}
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead>
                <tr className="border-b border-border text-xs uppercase tracking-wide text-neutral-500">
                  <th scope="col" className="px-4 py-3 font-medium">Nombre</th>
                  <th scope="col" className="px-4 py-3 font-medium">ID / URL</th>
                  <th scope="col" className="px-4 py-3 font-medium">Estado</th>
                  <th scope="col" className="px-4 py-3 font-medium">Última sync</th>
                  <th scope="col" className="px-4 py-3 text-right font-medium">Acciones</th>
                </tr>
              </thead>
              <tbody>
                {sources.map((source) => (
                  <tr key={source.id} className="border-b border-border/60 last:border-0">
                    <td className="px-4 py-3">
                      <p className="font-medium text-neutral-100">{source.channel_name}</p>
                      <Badge className="mt-1">{source.platform}</Badge>
                    </td>
                    <td className="px-4 py-3 text-neutral-400">{source.channel_id}</td>
                    <td className="px-4 py-3">
                      <Badge tone={source.active ? 'green' : 'neutral'}>{source.active ? 'activo' : 'inactivo'}</Badge>
                    </td>
                    <td className="px-4 py-3 text-neutral-400">{formatDateTime(source.last_checked_at)}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Sincronizar ${source.channel_name}`}
                          title="Encolar discovery"
                          loading={syncingId === source.id}
                          disabled={syncingId != null}
                          onClick={() => void handleSync(source)}
                        >
                          <RefreshCw aria-hidden="true" className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Editar ${source.channel_name}`}
                          title="Editar"
                          onClick={() => {
                            setEditing(source)
                            setFormOpen(true)
                          }}
                        >
                          <Pencil aria-hidden="true" className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={source.active ? `Desactivar ${source.channel_name}` : `Activar ${source.channel_name}`}
                          title={source.active ? 'Desactivar' : 'Activar'}
                          onClick={() => void handleToggle(source)}
                          disabled={syncingId != null}
                        >
                          <Power aria-hidden="true" className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Eliminar ${source.channel_name}`}
                          title="Eliminar"
                          onClick={() => setDeleting(source)}
                        >
                          <Trash2 aria-hidden="true" className="h-4 w-4 text-red-400" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </StateView>
      </Card>

      <ChannelFormDialog
        open={formOpen}
        onClose={() => {
          setFormOpen(false)
          setEditing(null)
        }}
        initial={editing}
        submitting={createSource.isPending || updateSource.isPending}
        onSubmit={handleSubmit}
      />

      <ConfirmDialog
        open={deleting != null}
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting) void deleteSource.mutateAsync(deleting.id).then(() => setDeleting(null))
        }}
        title="Eliminar canal"
        message={`¿Eliminar "${deleting?.channel_name}"? Sus clips detectados quedarán huérfanos.`}
        confirmLabel="Eliminar"
        loading={deleteSource.isPending}
      />
    </div>
  )
}