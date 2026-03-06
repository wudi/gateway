import { describe, it, expect } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../test/utils';
import { RoutesPage } from './Routes';
import { server } from '../test/mocks/handlers';
import { http, HttpResponse } from 'msw';

describe('RoutesPage', () => {
  it('renders route list', async () => {
    renderWithProviders(<RoutesPage />);
    await waitFor(() => {
      expect(screen.getByText('api-route')).toBeInTheDocument();
      expect(screen.getByText('web-route')).toBeInTheDocument();
      expect(screen.getByText('plain-route')).toBeInTheDocument();
    });
  });

  it('feature badges present for configured features', async () => {
    renderWithProviders(<RoutesPage />);
    await waitFor(() => {
      // api-route has CB and CA in the mock dashboard data
      expect(screen.getAllByText('CB').length).toBeGreaterThan(0);
      expect(screen.getAllByText('CA').length).toBeGreaterThan(0);
    });
  });

  it('click row opens detail panel', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => {
      expect(screen.getByText('api-route')).toBeInTheDocument();
    });
    await user.click(screen.getByText('api-route'));
    await waitFor(() => {
      expect(screen.getByLabelText(/details for api-route/i)).toBeInTheDocument();
    });
  });

  it('detail shows only configured features', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => {
      expect(screen.getByText('plain-route')).toBeInTheDocument();
    });
    await user.click(screen.getByText('plain-route'));
    await waitFor(() => {
      expect(screen.getByLabelText(/details for plain-route/i)).toBeInTheDocument();
    });
    const panel = screen.getByLabelText(/details for plain-route/i);
    expect(within(panel).queryByText('Circuit Breaker')).not.toBeInTheDocument();
    expect(within(panel).queryByText('Cache')).not.toBeInTheDocument();
  });

  it('detail shows CB state and action buttons', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => {
      expect(screen.getByText('api-route')).toBeInTheDocument();
    });
    await user.click(screen.getByText('api-route'));
    await waitFor(() => {
      expect(screen.getByText('Circuit Breaker')).toBeInTheDocument();
      expect(screen.getByText('Force Open')).toBeInTheDocument();
      expect(screen.getByText('Reset')).toBeInTheDocument();
    });
  });

  it('CB force-open shows ConfirmModal', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('api-route'));
    await user.click(screen.getByText('api-route'));
    await waitFor(() => screen.getByText('Force Open'));
    await user.click(screen.getByText('Force Open'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText(/force.open circuit breaker/i)).toBeInTheDocument();
  });

  it('cache purge shows ConfirmModal', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('api-route'));
    await user.click(screen.getByText('api-route'));
    await waitFor(() => screen.getByText('Purge Cache'));
    await user.click(screen.getByText('Purge Cache'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getAllByText(/purge/i).length).toBeGreaterThan(0);
  });

  it('Esc closes detail panel', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('api-route'));
    await user.click(screen.getByText('api-route'));
    await waitFor(() => {
      expect(screen.getByLabelText(/details for api-route/i)).toBeInTheDocument();
    });
    await user.keyboard('{Escape}');
    await waitFor(() => {
      expect(screen.queryByLabelText(/details for api-route/i)).not.toBeInTheDocument();
    });
  });

  it('detail panel shows path and backends count', async () => {
    const user = userEvent.setup();
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('api-route'));
    await user.click(screen.getByText('api-route'));
    await waitFor(() => {
      const panel = screen.getByLabelText(/details for api-route/i);
      expect(within(panel).getByText('Path')).toBeInTheDocument();
      expect(within(panel).getByText('Backends')).toBeInTheDocument();
    });
  });

  it('route with CB and cache shows both sections', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/routes', () =>
        HttpResponse.json([
          { id: 'full-route', path: '/full/*', backends: 2, path_prefix: false },
        ]),
      ),
      http.get('/dashboard', () =>
        HttpResponse.json({
          routes: { total: 1, healthy: 1 },
          'circuit-breakers': {
            'full-route': { state: 'closed', failures: 0, successes: 0 },
          },
          cache: { 'full-route': { hits: 10, misses: 5, size: 15, evictions: 0 } },
          uptime: '1h0m0s',
        }),
      ),
    );
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('full-route'));
    await user.click(screen.getByText('full-route'));
    await waitFor(() => {
      expect(screen.getByText('Circuit Breaker')).toBeInTheDocument();
      expect(screen.getByText('Cache')).toBeInTheDocument();
    });
  });
});
