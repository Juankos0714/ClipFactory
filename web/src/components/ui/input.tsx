import type { InputHTMLAttributes } from 'react'

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
  hint?: string
}

export function Input({ label, error, hint, id, className, ...rest }: InputProps) {
  const inputId = id ?? rest.name
  const describedBy = error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined
  return (
    <label className="flex flex-col gap-1" htmlFor={inputId}>
      {label ? <span className="text-sm font-medium text-neutral-300">{label}</span> : null}
      <input
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={`w-full rounded-md border bg-surface px-3 py-2 text-sm text-neutral-100 placeholder:text-neutral-500 focus:border-brand focus:outline-none ${
          error ? 'border-red-500/50' : 'border-border'
        } ${className ?? ''}`}
        {...rest}
      />
      {error ? (
        <span id={`${inputId}-error`} className="text-xs text-red-400" role="alert">
          {error}
        </span>
      ) : hint ? (
        <span id={`${inputId}-hint`} className="text-xs text-neutral-500">
          {hint}
        </span>
      ) : null}
    </label>
  )
}