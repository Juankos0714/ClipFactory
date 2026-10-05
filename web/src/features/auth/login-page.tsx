import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { KeyRound } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useAuth } from '@/providers/AuthProvider'

const loginSchema = z.object({
  token: z.string().trim().min(1, 'Ingresá el token de operador'),
})

type LoginValues = z.infer<typeof loginSchema>

/**
 * Login de operador (FASE 7, condicional).
 * El server aditivo usa un token único vía env (CLIPFACTORY_API_TOKEN). No hay
 * usuarios ni password: se ingresa el token y el backend lo valida en la
 * próxima petición. Si el server no exige token (dev), este flujo no aplica.
 */
export function LoginPage() {
  const { sessionActive, login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginValues>({ resolver: zodResolver(loginSchema), defaultValues: { token: '' } })

  // Tras autenticar vuelve a la ruta de origen; si el provider ya marcó la sesión
  // activa, este guard reemplaza la navegación del submit con el mismo destino.
  if (sessionActive) return <Navigate to={from ?? '/'} replace />

  const onSubmit = (values: LoginValues) => {
    login(values.token)
    void navigate(from ?? '/', { replace: true })
  }

  return (
    <div className="flex min-h-svh items-center justify-center bg-surface p-4">
      <Card className="w-full max-w-md">
        <div className="flex flex-col gap-4 p-6">
          <div className="flex flex-col items-center gap-2 text-center">
            <KeyRound aria-hidden="true" className="h-8 w-8 text-brand" />
            <h1 className="text-base font-semibold text-neutral-100">Autenticación de operador</h1>
            <p className="text-sm text-neutral-500">
              El server aditivo exige un token de API (<code className="rounded bg-black/20 px-1">CLIPFACTORY_API_TOKEN</code>).
              Ingresalo para operar el control center.
            </p>
          </div>

          <form className="flex flex-col gap-3" onSubmit={(e) => void handleSubmit(onSubmit)(e)} noValidate>
            <Input
              label="Token de API"
              type="password"
              autoComplete="off"
              placeholder="Bearer ••••••••"
              error={errors.token?.message}
              {...register('token')}
            />
            <Button type="submit">Iniciar sesión</Button>
          </form>

          <p className="text-center text-xs text-neutral-500">
            Si tu instalación no usa token (desarrollo), la app funciona sin iniciar sesión.
          </p>
        </div>
      </Card>
    </div>
  )
}