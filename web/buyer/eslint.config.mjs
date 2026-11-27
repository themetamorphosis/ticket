import nextVitals from 'eslint-config-next/core-web-vitals';
import { defineConfig, globalIgnores } from 'eslint/config';

export default defineConfig([
  ...nextVitals,
  globalIgnores(['.next/**', 'out/**', 'coverage/**']),
  {
    // Inline lint-disable comments are rejected: fix the code or change this config in review.
    linterOptions: { noInlineConfig: true, reportUnusedDisableDirectives: 'error' },
  },
]);
