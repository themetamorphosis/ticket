import { describe, expect, it } from 'vitest';
import { Providers } from '../components/Providers';
import RootLayout, { metadata, viewport } from './layout';

describe('RootLayout', () => {
  it('renders an English document with the theme around its children', () => {
    const tree = RootLayout({ children: 'content' });
    expect(tree.type).toBe('html');
    expect(tree.props.lang).toBe('en');
    const body = tree.props.children;
    expect(body.type).toBe('body');
    expect(body.props.children.type).toBe(Providers);
    expect(body.props.children.props.children).toBe('content');
  });

  it('extends under the safe area and sets a title', () => {
    expect(viewport.viewportFit).toBe('cover');
    expect(metadata.title).toMatch(/Kacheem Comms/);
  });
});
