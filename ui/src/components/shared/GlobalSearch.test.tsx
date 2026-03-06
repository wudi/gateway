import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { GlobalSearch } from './GlobalSearch';

const renderSearch = (props = {}) =>
  render(
    <MemoryRouter>
      <GlobalSearch routeIds={['api-route', 'web-route']} {...props} />
    </MemoryRouter>,
  );

// Helper to open search via Cmd+K
async function openSearch(user: ReturnType<typeof userEvent.setup>) {
  await user.keyboard('{Meta>}k{/Meta}');
}

describe('GlobalSearch', () => {
  it('Cmd+K opens modal', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('Ctrl+K opens modal', async () => {
    const user = userEvent.setup();
    renderSearch();
    await user.keyboard('{Control>}k{/Control}');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('filters as user types', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    await user.type(screen.getByLabelText('Search input'), 'route');
    const options = screen.getAllByRole('option');
    expect(options.length).toBeGreaterThan(0);
    options.forEach((opt) => {
      expect(opt.textContent?.toLowerCase()).toContain('route');
    });
  });

  it('arrow keys navigate results', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    const options = screen.getAllByRole('option');
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{ArrowDown}');
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{ArrowUp}');
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
  });

  it('Esc closes', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('no results for unmatched query', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    await user.type(screen.getByLabelText('Search input'), 'zzzznonexistent');
    expect(screen.getByText('No results')).toBeInTheDocument();
  });

  it('receives focus on open', async () => {
    const user = userEvent.setup();
    renderSearch();
    await openSearch(user);
    expect(document.activeElement).toBe(screen.getByLabelText('Search input'));
  });
});
