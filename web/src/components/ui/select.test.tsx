// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Select } from './select';

describe('Select', () => {
  it('expone la etiqueta como nombre accesible (no la lista de opciones)', () => {
    render(
      <Select
        label="Estado"
        name="status"
        placeholder="Todos"
        defaultValue=""
        options={[
          { value: 'queued', label: 'En cola' },
          { value: 'error', label: 'Error' },
        ]}
      />,
    );

    expect(screen.getByLabelText('Estado')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Estado' })).toBeInTheDocument();
  });

  it('renderiza placeholder y opciones', () => {
    render(
      <Select label="Plataforma" name="platform" placeholder="Todas" options={[{ value: 'twitch', label: 'Twitch' }]} />,
    );

    const select = screen.getByLabelText<HTMLSelectElement>('Plataforma');
    expect([...select.options].map((o) => o.textContent)).toEqual(['Todas', 'Twitch']);
  });

  it('funciona sin label ni name (id autogenerado)', () => {
    render(<Select options={[{ value: 'a', label: 'A' }]} />);

    expect(screen.getByRole('combobox')).toBeInTheDocument();
  });
});