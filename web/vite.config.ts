import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the API is proxied to the Go backend; in Docker nginx does the same.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': process.env.API_URL ?? 'http://localhost:8081',
    },
  },
});
