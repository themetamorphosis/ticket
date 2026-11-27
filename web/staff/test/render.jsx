import { render as rtlRender } from '@testing-library/react';
import { Providers } from '../components/Providers';

// Renders inside the app's theme, as the real layout does.
export function render(ui, options) {
  return rtlRender(ui, { wrapper: Providers, ...options });
}
