import { describe, it, expect } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../test/utils';
import { OperationsPage } from './Operations';

describe('OperationsPage', () => {
  it('reload button triggers POST /reload', async () => {
    const user = userEvent.setup();
    renderWithProviders(<OperationsPage />);
    await waitFor(() => screen.getByText('Reload Config'));
    await user.click(screen.getByText('Reload Config'));
    await waitFor(() => {
      expect(screen.getByText('Configuration reloaded successfully')).toBeInTheDocument();
    });
  });

  it('reload success shows inline banner', async () => {
    const user = userEvent.setup();
    renderWithProviders(<OperationsPage />);
    await waitFor(() => screen.getByText('Reload Config'));
    await user.click(screen.getByText('Reload Config'));
    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument();
      expect(screen.getByText('Configuration reloaded successfully')).toBeInTheDocument();
    });
  });

  it('reload history table populated', async () => {
    renderWithProviders(<OperationsPage />);
    await waitFor(() => {
      expect(screen.getByText('initial load')).toBeInTheDocument();
    });
  });

  it('drain toggle shows ConfirmModal', async () => {
    const user = userEvent.setup();
    renderWithProviders(<OperationsPage />);
    await waitFor(() => screen.getByText('Start Drain'));
    await user.click(screen.getByText('Start Drain'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText(/toggle drain/i)).toBeInTheDocument();
  });

  it('drain confirm sends POST /drain', async () => {
    const user = userEvent.setup();
    renderWithProviders(<OperationsPage />);
    await waitFor(() => screen.getByText('Start Drain'));
    await user.click(screen.getByText('Start Drain'));
    await user.type(screen.getByLabelText('Confirmation input'), 'drain');
    await user.click(screen.getByRole('button', { name: /confirm/i }));
    // Modal should close after confirm
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('maintenance toggle fires immediately', async () => {
    const user = userEvent.setup();
    renderWithProviders(<OperationsPage />);
    await waitFor(() => screen.getByText('Enable Maintenance'));
    await user.click(screen.getByText('Enable Maintenance'));
    // Should immediately show toggled state
    expect(screen.getByText('Disable Maintenance')).toBeInTheDocument();
  });
});
