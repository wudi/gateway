import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { EmptyState } from './EmptyState';

describe('EmptyState', () => {
  it('renders message', () => {
    render(<EmptyState message="No data available." />);
    expect(screen.getByText('No data available.')).toBeInTheDocument();
  });

  it('renders docs link when provided', () => {
    render(<EmptyState message="Nothing here." docsLink="https://docs.example.com" />);
    const link = screen.getByRole('link', { name: 'View documentation' });
    expect(link).toHaveAttribute('href', 'https://docs.example.com');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('does not render link when not provided', () => {
    render(<EmptyState message="Nothing here." />);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });
});
