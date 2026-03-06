import './index.css';
import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { PollingProvider } from './context/PollingContext';
import { Sidebar } from './components/layout/Sidebar';
import { GlobalSearch } from './components/shared/GlobalSearch';
import { StatusPage } from './pages/Status';
import { RoutesPage } from './pages/Routes';
import { InfrastructurePage } from './pages/Infrastructure';
import { TrafficControlPage } from './pages/TrafficControl';
import { DeploymentsPage } from './pages/Deployments';
import { SecurityPage } from './pages/Security';
import { OperationsPage } from './pages/Operations';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <PollingProvider>
        <BrowserRouter basename="/ui">
          <div className="flex min-h-screen">
            <aside className="w-[220px] border-r border-border">
              <Sidebar />
            </aside>
            <main className="flex-1 p-6">
              <GlobalSearch />
              <Routes>
                <Route path="/" element={<StatusPage />} />
                <Route path="/routes" element={<RoutesPage />} />
                <Route path="/infrastructure" element={<InfrastructurePage />} />
                <Route path="/traffic" element={<TrafficControlPage />} />
                <Route path="/deployments" element={<DeploymentsPage />} />
                <Route path="/security" element={<SecurityPage />} />
                <Route path="/operations" element={<OperationsPage />} />
              </Routes>
            </main>
          </div>
        </BrowserRouter>
      </PollingProvider>
    </QueryClientProvider>
  </React.StrictMode>,
);
