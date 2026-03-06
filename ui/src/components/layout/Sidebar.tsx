import { NavLink } from 'react-router-dom';
import { clsx } from 'clsx';
import {
  Activity,
  Route,
  Server,
  Gauge,
  GitBranch,
  Shield,
  Settings,
} from 'lucide-react';

const navItems = [
  { path: '/', label: 'Status', icon: Activity, end: true },
  { path: '/routes', label: 'Routes', icon: Route },
  { path: '/infrastructure', label: 'Infrastructure', icon: Server },
  { path: '/traffic', label: 'Traffic Control', icon: Gauge },
  { path: '/deployments', label: 'Deployments', icon: GitBranch },
  { path: '/security', label: 'Security', icon: Shield },
  { path: '/operations', label: 'Operations', icon: Settings },
];

export function Sidebar() {
  return (
    <nav role="navigation" aria-label="Main navigation" className="flex flex-col gap-1 p-3">
      {navItems.map((item) => (
        <NavLink
          key={item.path}
          to={item.path}
          end={item.end}
          className={({ isActive }) =>
            clsx(
              'flex items-center gap-3 px-3 py-2 rounded-md text-sm transition-colors duration-150',
              isActive
                ? 'bg-bg-elevated text-text-primary'
                : 'text-text-secondary hover:text-text-primary hover:bg-bg-elevated/50',
            )
          }
        >
          {({ isActive }) => (
            <>
              <item.icon className="h-4 w-4" />
              <span aria-current={isActive ? 'page' : undefined}>{item.label}</span>
            </>
          )}
        </NavLink>
      ))}
    </nav>
  );
}
