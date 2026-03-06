import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '../test/utils';
import { StatusPage } from './Status';
import { server } from '../test/mocks/handlers';
import { http, HttpResponse } from 'msw';
import { createDegradedDashboard } from '../test/mocks/fixtures/dashboard';


describe('StatusPage', () => {
  it('green banner when zero problems', async () => {
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText('All systems operational')).toBeInTheDocument();
    });
  });

  it('problems table when CB open', async () => {
    const degraded = createDegradedDashboard();
    server.use(
      http.get('/dashboard', () => HttpResponse.json(degraded)),
    );
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText('Circuit Breaker')).toBeInTheDocument();
      expect(screen.getAllByText('api-route').length).toBeGreaterThan(0);
    });
  });

  it('problems table when backend down', async () => {
    const degraded = createDegradedDashboard();
    server.use(
      http.get('/dashboard', () => HttpResponse.json(degraded)),
    );
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText(/unhealthy/i)).toBeInTheDocument();
    });
  });

  it('system summary row shows uptime, routes, and listeners', async () => {
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText('Uptime')).toBeInTheDocument();
      expect(screen.getByText('Routes')).toBeInTheDocument();
      expect(screen.getByText('Listeners')).toBeInTheDocument();
    });
  });

  it('recent events section renders', async () => {
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText('Recent Events')).toBeInTheDocument();
    });
  });

  it('"View" link navigates to Routes with route selected', async () => {
    const degraded = createDegradedDashboard();
    server.use(
      http.get('/dashboard', () => HttpResponse.json(degraded)),
    );
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      const links = screen.getAllByText('View');
      expect(links.length).toBeGreaterThan(0);
      expect(links[0].closest('a')).toHaveAttribute(
        'href',
        expect.stringContaining('/routes'),
      );
    });
  });

  it('API outage shows connection error', async () => {
    server.use(
      http.get('/dashboard', () => HttpResponse.json({}, { status: 502 })),
      http.get('/health', () => HttpResponse.json({}, { status: 502 })),
    );
    renderWithProviders(<StatusPage />);
    await waitFor(() => {
      expect(screen.getByText(/failed to connect/i)).toBeInTheDocument();
    });
  });
});
