import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import StaffHomePage from './page';

describe('staff HomePage', () => {
  it('identifies the staff console and its VPN-only access', () => {
    render(<StaffHomePage />);
    expect(screen.getByRole('heading', { level: 1, name: 'Staff console' })).toBeInTheDocument();
    expect(screen.getByText(/staff VPN/)).toBeInTheDocument();
  });
});
