import { describe, expect, it } from 'vitest';
import nextConfig from './next.config.mjs';

describe('next.config', () => {
  it('builds a static export that NGINX can serve without a Node server', () => {
    expect(nextConfig.output).toBe('export');
    expect(nextConfig.trailingSlash).toBe(true);
    expect(nextConfig.images.unoptimized).toBe(true);
    expect(nextConfig.poweredByHeader).toBe(false);
  });
});
