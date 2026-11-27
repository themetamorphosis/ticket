import Button from '@mui/material/Button';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../lib/api';
import { me } from '../test/apiMock';
import { render } from '../test/render';
import { PageHeader } from './PageHeader';
import { ReasonAction } from './ReasonAction';
import { StaffShell } from './StaffShell';
import { StatusChip } from './StatusChip';

vi.mock('../lib/api', async (importOriginal) => (await import('../test/apiMock')).apiMock(await importOriginal()));
vi.mock('next/navigation', async (importOriginal) => ({ ...(await importOriginal()), usePathname: () => '/scan' }));

beforeEach(() => Object.values(api).forEach((f) => f.mockReset()));

describe('StaffShell chrome', () => {
  it('lifts the page header into the app bar and marks the current page', async () => {
    api.me.mockResolvedValue(me('organizer'));
    render(
      <StaffShell roles={['organizer']}>
        {() => <PageHeader title="Scanner" actions={<Button>Act</Button>} />}
      </StaffShell>,
    );
    const bar = await screen.findByRole('banner');
    await waitFor(() => expect(within(bar).getByRole('heading', { name: 'Scanner' })).toBeInTheDocument());
    expect(within(bar).getByRole('button', { name: 'Act' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Scanner' })).toHaveClass('Mui-selected');
    expect(screen.getByRole('link', { name: 'Overview' })).not.toHaveClass('Mui-selected');
  });

  it('opens the navigation drawer and closes it on navigation', async () => {
    api.me.mockResolvedValue(me('gate'));
    render(<StaffShell roles={['gate']}>{() => <p>page</p>}</StaffShell>);
    await screen.findByText('page');
    // The menu button only shows below md; the test DOM is desktop width.
    fireEvent.click(screen.getByLabelText('Open navigation'));
    const link = await waitFor(() => {
      const a = document.querySelector('.MuiDrawer-modal a[href^="/scan"]');
      expect(a).not.toBeNull();
      return a;
    });
    fireEvent.click(link);
    await waitFor(() => expect(document.querySelector('.MuiDrawer-modal')).toBeNull());
  });
});

describe('ReasonAction without a reason', () => {
  it('reports a failure beside the button', async () => {
    const user = userEvent.setup();
    const action = vi.fn().mockRejectedValue(new ApiError(500, 'internal', 'Could not resend.'));
    render(<ReasonAction label="Resend" needsReason={false} action={action} onDone={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Resend' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Could not resend.');
  });
});

describe('StatusChip', () => {
  it('makes the status readable and colours it', () => {
    render(<><StatusChip status="already_used" /><StatusChip status="paid" /></>);
    expect(screen.getByText('already used').closest('.MuiChip-root')).toHaveClass('MuiChip-colorDefault');
    expect(screen.getByText('paid').closest('.MuiChip-root')).toHaveClass('MuiChip-colorSuccess');
  });
});
