import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '../test/utils';
import { DeploymentsPage } from './Deployments';

describe('DeploymentsPage', () => {
  it('empty state when no deployments', async () => {
    renderWithProviders(<DeploymentsPage />);
    await waitFor(() => {
      expect(screen.getByText('No active deployments.')).toBeInTheDocument();
    });
  });
});
