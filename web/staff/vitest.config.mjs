import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./test/setup.js'],
    include: ['**/*.test.{js,jsx}'],
    exclude: ['node_modules/**', '.next/**', 'out/**'],
    coverage: {
      provider: 'v8',
      include: ['app/**/*.{js,jsx}', 'components/**/*.{js,jsx}', 'lib/**/*.{js,jsx}', 'theme/**/*.{js,jsx}', 'next.config.mjs'],
      exclude: ['**/*.test.{js,jsx}'],
      reporter: ['text', 'html'],
      // The 100% gate (plan section 9). Untestable code is restructured, never ignored.
      thresholds: { lines: 100, statements: 100, functions: 100, branches: 100 },
    },
  },
});
