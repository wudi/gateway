import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../test/utils';
import { InfrastructurePage } from './Infrastructure';
import { server } from '../test/mocks/handlers';
import { http, HttpResponse } from 'msw';

describe('InfrastructurePage', () => {
  it('renders tabs: Backends, Listeners, Certificates, Upstreams', async () => {
    renderWithProviders(<InfrastructurePage />);
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Backends' })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: 'Listeners' })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: 'Certificates' })).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: 'Upstreams' })).toBeInTheDocument();
    });
  });

  it('backends tab sorted unhealthy-first', async () => {
    server.use(
      http.get('/backends', () =>
        HttpResponse.json([
          { url: 'http://10.0.0.1:8080', route_id: 'r1', healthy: true, response_time_ms: 10, active_connections: 1, total_requests: 100 },
          { url: 'http://10.0.0.2:8080', route_id: 'r1', healthy: false, response_time_ms: 0, active_connections: 0, total_requests: 50 },
        ]),
      ),
    );
    renderWithProviders(<InfrastructurePage />);
    await waitFor(() => {
      const rows = screen.getAllByRole('row');
      // First data row should be the unhealthy one
      expect(rows[1]).toHaveTextContent('10.0.0.2');
    });
  });

  it('certificates tab highlights expiring certs', async () => {
    const user = userEvent.setup();
    renderWithProviders(<InfrastructurePage />);
    await waitFor(() => screen.getByRole('tab', { name: 'Certificates' }));
    await user.click(screen.getByRole('tab', { name: 'Certificates' }));
    await waitFor(() => {
      expect(screen.getByText('26d remaining')).toBeInTheDocument();
    });
  });

  it('expired cert shows critical state', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/certificates', () =>
        HttpResponse.json([
          { hostname: 'expired.example.com', issuer: "Let's Encrypt", not_after: '2025-01-01', days_remaining: 0 },
        ]),
      ),
    );
    renderWithProviders(<InfrastructurePage />);
    await waitFor(() => screen.getByRole('tab', { name: 'Certificates' }));
    await user.click(screen.getByRole('tab', { name: 'Certificates' }));
    await waitFor(() => {
      expect(screen.getByText('Expired')).toBeInTheDocument();
    });
  });

  it('tab switching preserves data', async () => {
    const user = userEvent.setup();
    renderWithProviders(<InfrastructurePage />);
    await waitFor(() => screen.getByRole('tab', { name: 'Backends' }));

    // Switch to listeners
    await user.click(screen.getByRole('tab', { name: 'Listeners' }));
    await waitFor(() => screen.getByText('http-main'));

    // Switch back to backends
    await user.click(screen.getByRole('tab', { name: 'Backends' }));
    await waitFor(() => {
      // Data should still be there without loading spinner
      const rows = screen.getAllByRole('row');
      expect(rows.length).toBeGreaterThan(1);
    });
  });

  it('deep-link via ?tab=certificates', async () => {
    renderWithProviders(<InfrastructurePage />, {
      routerProps: { initialEntries: ['/ui/infrastructure?tab=certificates'] },
    });
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Certificates' })).toHaveAttribute('aria-selected', 'true');
    });
  });
});
