// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useJobsList, useJobsStats } from './use-jobs';
import { jobsApi } from '@/lib/api/resources';
import type { Job, Paginated, SystemOverview } from '@/types/api';

vi.mock('@/lib/api/resources', () => ({
  jobsApi: {
    list: vi.fn(),
    stats: vi.fn(),
  },
}));

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe('useJobs hooks', () => {
  it('useJobsList fetches jobs list', async () => {
    const mockData: Paginated<Job> = {
      data: [],
      pagination: { page: 1, pageSize: 50, total: 0, pageCount: 0, hasNext: false, hasPrev: false },
    };
    vi.mocked(jobsApi.list).mockResolvedValueOnce(mockData);

    const { result } = renderHook(() => useJobsList(), { wrapper });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(result.current.data).toEqual(mockData);
  });

  it('useJobsStats fetches job stats', async () => {
    const mockStats: SystemOverview['jobs'] = {
      discovery: { queued: 0, running: 0, done: 5, error: 0 },
    };
    vi.mocked(jobsApi.stats).mockResolvedValueOnce(mockStats);

    const { result } = renderHook(() => useJobsStats(), { wrapper });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(result.current.data).toEqual(mockStats);
  });
});