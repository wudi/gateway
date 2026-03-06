import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DataTable, type Column } from './DataTable';

interface TestRow {
  id: string;
  name: string;
  count: number;
}

const testData: TestRow[] = [
  { id: '1', name: 'Alpha', count: 30 },
  { id: '2', name: 'Beta', count: 10 },
  { id: '3', name: 'Gamma', count: 20 },
];

const columns: Column<TestRow>[] = [
  {
    key: 'name',
    header: 'Name',
    render: (row) => row.name,
    sortable: true,
    sortFn: (a, b) => a.name.localeCompare(b.name),
  },
  {
    key: 'count',
    header: 'Count',
    render: (row) => row.count,
    numeric: true,
    sortable: true,
    sortFn: (a, b) => a.count - b.count,
  },
];

describe('DataTable', () => {
  it('renders rows from data prop', () => {
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    const rows = screen.getAllByRole('row');
    // 1 header + 3 data rows
    expect(rows.length).toBe(4);
  });

  it('sorts by column header click', async () => {
    const user = userEvent.setup();
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    await user.click(screen.getByText('Count'));
    const rows = screen.getAllByRole('row');
    const firstDataRow = rows[1];
    expect(within(firstDataRow).getByText('Beta')).toBeInTheDocument();
  });

  it('reverse-sorts on second click', async () => {
    const user = userEvent.setup();
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    await user.click(screen.getByText('Count'));
    await user.click(screen.getByText('Count'));
    const rows = screen.getAllByRole('row');
    const firstDataRow = rows[1];
    expect(within(firstDataRow).getByText('Alpha')).toBeInTheDocument();
  });

  it('filters rows via search input', async () => {
    const user = userEvent.setup();
    render(
      <DataTable
        data={testData}
        columns={columns}
        rowKey={(r) => r.id}
        searchable
        searchFn={(row, q) => row.name.toLowerCase().includes(q.toLowerCase())}
      />,
    );
    await user.type(screen.getByLabelText('Filter table'), 'alp');
    const rows = screen.getAllByRole('row');
    // header + 1 matching row
    expect(rows.length).toBe(2);
    expect(screen.getByText('Alpha')).toBeInTheDocument();
  });

  it('j/k keyboard navigation', async () => {
    const user = userEvent.setup();
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    const table = screen.getByRole('table');
    table.focus();
    await user.keyboard('j');
    const rows = screen.getAllByRole('row');
    expect(rows[1]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('j');
    expect(rows[2]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('k');
    expect(rows[1]).toHaveAttribute('aria-selected', 'true');
  });

  it('Enter selects highlighted row', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(
      <DataTable
        data={testData}
        columns={columns}
        rowKey={(r) => r.id}
        onSelect={onSelect}
      />,
    );
    const table = screen.getByRole('table');
    table.focus();
    await user.keyboard('j');
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledWith(testData[0]);
  });

  it('empty data shows EmptyState', () => {
    render(
      <DataTable
        data={[]}
        columns={columns}
        rowKey={(r: TestRow) => r.id}
        emptyMessage="Nothing to show"
      />,
    );
    expect(screen.getByText('Nothing to show')).toBeInTheDocument();
  });

  it('rows have correct ARIA attributes', () => {
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    const rows = screen.getAllByRole('row');
    rows.forEach((row) => {
      expect(row).toHaveAttribute('role', 'row');
    });
  });

  it('numeric columns have data-numeric attribute', () => {
    render(
      <DataTable data={testData} columns={columns} rowKey={(r) => r.id} />,
    );
    const numericCells = document.querySelectorAll('[data-numeric="true"]');
    expect(numericCells.length).toBe(3); // 3 data rows × 1 numeric column
  });
});
