import { describe, it, expect } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Sidebar } from './Sidebar';

const renderSidebar = (initialPath = '/ui') =>
  render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Sidebar />
    </MemoryRouter>,
  );

describe('Sidebar', () => {
  it('renders 7 nav links', () => {
    renderSidebar();
    const nav = screen.getByRole('navigation');
    const links = within(nav).getAllByRole('link');
    expect(links.length).toBe(7);
  });

  it('highlights active route', () => {
    renderSidebar('/ui/routes');
    expect(screen.getByText('Routes')).toHaveAttribute('aria-current', 'page');
  });

  it('nav links use correct hrefs', () => {
    renderSidebar();
    const expectedPaths = [
      '/ui',
      '/ui/routes',
      '/ui/infrastructure',
      '/ui/traffic',
      '/ui/deployments',
      '/ui/security',
      '/ui/operations',
    ];
    const nav = screen.getByRole('navigation');
    const links = within(nav).getAllByRole('link');
    links.forEach((link, i) => {
      expect(link).toHaveAttribute('href', expectedPaths[i]);
    });
  });

  it('Status is active on /ui', () => {
    renderSidebar('/ui');
    expect(screen.getByText('Status')).toHaveAttribute('aria-current', 'page');
  });
});
