import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: '/ui/',
  server: {
    proxy: {
      '/dashboard': 'http://localhost:8081',
      '/health': 'http://localhost:8081',
      '/routes': 'http://localhost:8081',
      '/backends': 'http://localhost:8081',
      '/listeners': 'http://localhost:8081',
      '/certificates': 'http://localhost:8081',
      '/circuit-breakers': 'http://localhost:8081',
      '/rate-limits': 'http://localhost:8081',
      '/traffic-shaping': 'http://localhost:8081',
      '/rules': 'http://localhost:8081',
      '/drain': 'http://localhost:8081',
      '/reload': 'http://localhost:8081',
      '/cache': 'http://localhost:8081',
      '/upstreams': 'http://localhost:8081',
      '/stats': 'http://localhost:8081',
    },
  },
});
