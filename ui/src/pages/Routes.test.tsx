import { describe, it, expect, vi } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../test/utils';
import { RoutesPage } from './Routes';
import { server } from '../test/mocks/handlers';
import { http, HttpResponse } from 'msw';
import { allFeaturesRoute, zeroBackendsRoute } from '../test/mocks/fixtures/edge-cases';
import { defaultRoutes } from '../test/mocks/fixtures/routes';

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
      expect(screen.getAllByText('CB').length).toBeGreaterThan(0);
      expect(screen.getAllByText('CA').length).toBeGreaterThan(0);
      expect(screen.getAllByText('RT').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('RL').length).toBeGreaterThan(0);
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

  it('route with zero backends shows degraded state', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/routes', () =>
        HttpResponse.json([...defaultRoutes, zeroBackendsRoute]),
      ),
      http.get('/dashboard', () =>
        HttpResponse.json({
          routes: [
            { id: 'no-backends-route', path: '/orphan', backends: 0, healthy_backends: 0, total_requests: 0, error_rate: 0, features: [] },
          ],
          backends: [],
          circuit_breakers: {},
          rate_limits: {},
          cache: {},
          uptime_seconds: 100,
          total_requests: 0,
          active_connections: 0,
        }),
      ),
    );
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('no-backends-route'));
    await user.click(screen.getByText('no-backends-route'));
    await waitFor(() => {
      expect(screen.getByText('No backends')).toBeInTheDocument();
    });
  });

  it('route with ALL features shows all sections', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/routes', () =>
        HttpResponse.json([...defaultRoutes, allFeaturesRoute]),
      ),
      http.get('/dashboard', () =>
        HttpResponse.json({
          routes: [
            {
              id: 'all-features-route',
              path: '/all/*',
              backends: 2,
              healthy_backends: 2,
              total_requests: 100,
              error_rate: 0,
              features: ['CB', 'CA', 'RT', 'RL', 'WS', 'TH'],
            },
          ],
          backends: [],
          circuit_breakers: {
            'all-features-route': { state: 'closed', failures: 0, successes: 0, consecutive_failures: 0 },
          },
          rate_limits: {},
          cache: { 'all-features-route': { hits: 0, misses: 0, size: 0, evictions: 0 } },
          uptime_seconds: 100,
          total_requests: 100,
          active_connections: 0,
        }),
      ),
    );
    renderWithProviders(<RoutesPage />);
    await waitFor(() => screen.getByText('all-features-route'));
    await user.click(screen.getByText('all-features-route'));
    await waitFor(() => {
      expect(screen.getByText('Circuit Breaker')).toBeInTheDocument();
      expect(screen.getByText('Cache')).toBeInTheDocument();
      expect(screen.getByText('Retry')).toBeInTheDocument();
      expect(screen.getByText('Rate Limit')).toBeInTheDocument();
      expect(screen.getByText('WebSocket')).toBeInTheDocument();
      expect(screen.getByText('Throttle')).toBeInTheDocument();
    });
  });
});
