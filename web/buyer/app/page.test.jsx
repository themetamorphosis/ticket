import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import HomePage from './page';

describe('buyer HomePage', () => {
  it('shows the organizer and the pre-announcement state', () => {
    render(<HomePage />);
    expect(screen.getByRole('heading', { level: 1, name: 'Kacheem Comms' })).toBeInTheDocument();
    expect(screen.getByText(/Tickets go on sale once the event is announced/)).toBeInTheDocument();
    expect(screen.getByRole('main').querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
  });
});
