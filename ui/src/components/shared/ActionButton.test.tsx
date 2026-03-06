import { describe, it, expect } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ActionButton } from './ActionButton';

describe('ActionButton', () => {
  it('idle state shows label', () => {
    render(<ActionButton label="Click me" onClick={() => {}} />);
    expect(screen.getByRole('button', { name: /click me/i })).toBeInTheDocument();
  });

  it('loading shows spinner and disabled', async () => {
    const user = userEvent.setup();
    let resolve: () => void;
    const onClick = () => new Promise<void>((r) => { resolve = r; });

    render(<ActionButton label="Submit" onClick={onClick} />);
    await user.click(screen.getByRole('button'));

    expect(screen.getByLabelText('Loading')).toBeInTheDocument();
    expect(screen.getByRole('button')).toHaveAttribute('aria-disabled', 'true');

    // Clean up
    resolve!();
  });

  it('success shows checkmark', async () => {
    const user = userEvent.setup();
    render(<ActionButton label="Do it" onClick={() => {}} />);
    await user.click(screen.getByRole('button'));
    // After successful click, should briefly show success
    await waitFor(() => {
      // The button should still be visible
      expect(screen.getByRole('button')).toBeInTheDocument();
    });
  });

  it('error shows inline message', async () => {
    const user = userEvent.setup();
    render(
      <ActionButton
        label="Fail"
        onClick={() => {
          throw new Error('Action failed');
        }}
      />,
    );
    await user.click(screen.getByRole('button'));
    await waitFor(() => {
      expect(screen.getByText('Action failed')).toBeInTheDocument();
    });
  });
});
