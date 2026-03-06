import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ConfirmModal } from './ConfirmModal';

describe('ConfirmModal', () => {
  const defaultProps = {
    title: 'Confirm Action',
    onConfirm: vi.fn(),
    onCancel: vi.fn(),
  };

  it('renders as modal dialog', () => {
    render(<ConfirmModal {...defaultProps} />);
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
  });

  it('traps focus inside modal', async () => {
    const user = userEvent.setup();
    render(<ConfirmModal {...defaultProps} />);
    const dialog = screen.getByRole('dialog');
    const focusableElements = dialog.querySelectorAll('button');
    const lastButton = focusableElements[focusableElements.length - 1];

    // Focus last element and tab — should cycle back
    (lastButton as HTMLElement).focus();
    await user.tab();
    // Focus should be on first focusable element inside dialog
    expect(document.activeElement).toBeTruthy();
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  it('confirm disabled until name typed', async () => {
    const user = userEvent.setup();
    render(
      <ConfirmModal
        {...defaultProps}
        requireTypedConfirmation="my-route"
      />,
    );
    const confirmBtn = screen.getByRole('button', { name: /confirm/i });
    expect(confirmBtn).toBeDisabled();

    await user.type(screen.getByLabelText('Confirmation input'), 'my-route');
    expect(confirmBtn).not.toBeDisabled();
  });

  it('exact match enables confirm', async () => {
    const user = userEvent.setup();
    render(
      <ConfirmModal
        {...defaultProps}
        requireTypedConfirmation="test"
      />,
    );
    const confirmBtn = screen.getByRole('button', { name: /confirm/i });

    await user.type(screen.getByLabelText('Confirmation input'), 'tes');
    expect(confirmBtn).toBeDisabled();

    await user.type(screen.getByLabelText('Confirmation input'), 't');
    expect(confirmBtn).not.toBeDisabled();
  });

  it('onConfirm callback fires', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<ConfirmModal {...defaultProps} onConfirm={onConfirm} />);
    await user.click(screen.getByRole('button', { name: /confirm/i }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('Esc fires onCancel', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<ConfirmModal {...defaultProps} onCancel={onCancel} />);
    await user.keyboard('{Escape}');
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it('spinner during pending', () => {
    render(<ConfirmModal {...defaultProps} isPending />);
    expect(screen.getByLabelText('Loading')).toBeInTheDocument();
  });

  it('error banner on failure', () => {
    render(<ConfirmModal {...defaultProps} error="Something went wrong" />);
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });
});
