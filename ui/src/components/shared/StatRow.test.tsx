import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatRow } from './StatRow';

describe('StatRow', () => {
  it('renders all items', () => {
    render(
      <StatRow
        items={[
          { label: 'Uptime', value: '24h' },
          { label: 'Requests', value: 1000 },
        ]}
      />,
    );
    expect(screen.getByText('Uptime')).toBeInTheDocument();
    expect(screen.getByText('24h')).toBeInTheDocument();
    expect(screen.getByText('Requests')).toBeInTheDocument();
    expect(screen.getByText('1000')).toBeInTheDocument();
  });

  it('numeric values have tabular-nums', () => {
    render(<StatRow items={[{ label: 'Count', value: 42 }]} />);
    const valueEl = screen.getByText('42');
    expect(valueEl).toHaveAttribute('data-numeric', 'true');
  });
});
