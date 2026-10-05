import { useId, type SelectHTMLAttributes } from 'react'

export interface SelectOption {
  value: string
  label: string
}

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string
  options: SelectOption[]
  placeholder?: string
}

/**
 * Select con etiqueta asociada por `aria-labelledby` (no `<label>` envolvente):
 * si el `<select>` queda dentro del `<label>`, el nombre accesible incluiría el
 * texto de todos los `<option>` ("EstadoTodosEn cola…") y un lector de pantalla
 * anunciaría la lista de valores en lugar de la etiqueta.
 */
export function Select({ label, options, placeholder, id, className, name, ...rest }: SelectProps) {
  const autoId = useId()
  const selectId = id ?? name ?? autoId
  const labelId = `${selectId}-label`

  return (
    <div className="flex flex-col gap-1">
      {label ? (
        <span id={labelId} className="text-sm font-medium text-neutral-300">
          {label}
        </span>
      ) : null}
      <select
        id={selectId}
        name={name}
        aria-labelledby={label ? labelId : undefined}
        className={`w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-neutral-100 focus:border-brand focus:outline-none ${className ?? ''}`}
        {...rest}
      >
        {placeholder ? <option value="">{placeholder}</option> : null}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  )
}