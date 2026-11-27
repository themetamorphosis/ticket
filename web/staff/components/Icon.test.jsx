import { Ticket01Icon } from '@hugeicons/core-free-icons';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ICON_SIZE, ICON_STROKE, Icon } from './Icon';

describe('Icon', () => {
  it('is decorative by default with the fixed size and stroke', () => {
    const { container } = render(<Icon icon={Ticket01Icon} />);
    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).not.toHaveAttribute('role');
    expect(svg).toHaveAttribute('width', String(ICON_SIZE));
    expect(svg).toHaveAttribute('stroke-width', String(ICON_STROKE));
    expect(svg).toHaveAttribute('focusable', 'false');
  });

  it('is announced when given a label', () => {
    const { getByRole } = render(<Icon icon={Ticket01Icon} label="Ticket" size={32} className="big" />);
    const svg = getByRole('img', { name: 'Ticket' });
    expect(svg).not.toHaveAttribute('aria-hidden');
    expect(svg).toHaveAttribute('width', '32');
    expect(svg).toHaveClass('big');
  });
});
