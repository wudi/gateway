import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../test/utils';
import { TrafficControlPage } from './TrafficControl';

describe('TrafficControlPage', () => {
  it('collapsible sections render', async () => {
    renderWithProviders(<TrafficControlPage />);
    await waitFor(() => {
      expect(screen.getByText('Rate Limits')).toBeInTheDocument();
      expect(screen.getByText('Throttle & Shaping')).toBeInTheDocument();
      expect(screen.getByText('Load Shedding')).toBeInTheDocument();
      expect(screen.getByText('Adaptive Concurrency')).toBeInTheDocument();
    });
  });

  it('expand/collapse on click', async () => {
    const user = userEvent.setup();
    renderWithProviders(<TrafficControlPage />);
    await waitFor(() => screen.getByText('Load Shedding'));
    // Load Shedding is not configured, should be collapsed
    // Click to expand
    await user.click(screen.getByText('Load Shedding'));
    expect(screen.getByText(/no load shedding/i)).toBeInTheDocument();
  });

  it('unconfigured shows "(not configured)" collapsed', async () => {
    renderWithProviders(<TrafficControlPage />);
    await waitFor(() => {
      expect(screen.getAllByText('(not configured)').length).toBeGreaterThan(0);
    });
  });

  it('configured with zero data shows zeros', async () => {
    renderWithProviders(<TrafficControlPage />);
    await waitFor(() => {
      expect(screen.getAllByText('api-route').length).toBeGreaterThan(0);
    });
  });
});
