// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { ProtectedRoute } from './protected-route';

/** ProtectedRoute solo lee estos dos campos del contexto. */
type AuthStub = { sessionActive: boolean; serverRequiresAuth: boolean };
const mockUseAuth = vi.fn<() => AuthStub>();

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => mockUseAuth(),
}));

describe('ProtectedRoute', () => {
  it('redirects to login when not authenticated', () => {
    mockUseAuth.mockReturnValue({ sessionActive: false, serverRequiresAuth: true });
    render(
      <MemoryRouter initialEntries={['/dashboard']}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route element={<ProtectedRoute />}>
            <Route path="/dashboard" element={<div>Dashboard</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText('Login Page')).toBeInTheDocument();
  });

  it('renders protected content when authenticated', () => {
    mockUseAuth.mockReturnValue({ sessionActive: true, serverRequiresAuth: true });
    render(
      <MemoryRouter initialEntries={['/dashboard']}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route element={<ProtectedRoute />}>
            <Route path="/dashboard" element={<div>Dashboard Content</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText('Dashboard Content')).toBeInTheDocument();
  });
});
