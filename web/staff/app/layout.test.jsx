import { describe, expect, it } from 'vitest';
import RootLayout, { metadata, viewport } from './layout';

describe('RootLayout', () => {
  it('renders an English document around its children', () => {
    const tree = RootLayout({ children: 'content' });
    expect(tree.type).toBe('html');
    expect(tree.props.lang).toBe('en');
    expect(tree.props.children.type).toBe('body');
    expect(tree.props.children.props.children).toBe('content');
  });

  it('extends under the safe area and sets a title', () => {
    expect(viewport.viewportFit).toBe('cover');
    expect(metadata.title).toMatch(/Kacheem Comms/);
  });
});
