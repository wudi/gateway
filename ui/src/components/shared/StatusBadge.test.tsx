import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge } from './StatusBadge';

describe('StatusBadge', () => {
  it('"healthy" has accessible label', () => {
    render(<StatusBadge status="healthy" />);
    expect(screen.getByLabelText('healthy')).toBeInTheDocument();
  });

  it('"degraded" has accessible label', () => {
    render(<StatusBadge status="degraded" />);
    expect(screen.getByLabelText('degraded')).toBeInTheDocument();
  });

  it('"down" has accessible label', () => {
    render(<StatusBadge status="down" />);
    expect(screen.getByLabelText('down')).toBeInTheDocument();
  });

  it('renders with data-status attribute', () => {
    render(<StatusBadge status="healthy" />);
    const badge = screen.getByLabelText('healthy');
    expect(badge).toHaveAttribute('data-status', 'healthy');
  });

  it('uses custom label when provided', () => {
    render(<StatusBadge status="healthy" label="All good" />);
    expect(screen.getByLabelText('All good')).toBeInTheDocument();
  });
});
