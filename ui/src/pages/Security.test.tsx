import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '../test/utils';
import { SecurityPage } from './Security';

describe('SecurityPage', () => {
  it('WAF and rules sections render', async () => {
    renderWithProviders(<SecurityPage />);
    await waitFor(() => {
      expect(screen.getByText('WAF')).toBeInTheDocument();
      expect(screen.getByText('Rules Engine')).toBeInTheDocument();
    });
  });

  it('cert expiry alerts cross-referenced', async () => {
    renderWithProviders(<SecurityPage />);
    await waitFor(() => {
      expect(screen.getByText('Certificate Expiry Alerts')).toBeInTheDocument();
      expect(screen.getByText(/api\.example\.com/)).toBeInTheDocument();
    });
  });
});
