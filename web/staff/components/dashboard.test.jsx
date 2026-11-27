import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../lib/api';
import { me } from '../test/apiMock';
import { AuditLog } from './AuditLog';
import { EventEditor } from './EventEditor';
import { Finance } from './Finance';
import { Orders } from './Orders';
import { Overview } from './Overview';
import { People } from './People';
import { ReasonAction } from './ReasonAction';

vi.mock('../lib/api', async (importOriginal) => (await import('../test/apiMock')).apiMock(await importOriginal()));

beforeEach(() => Object.values(api).forEach((f) => f.mockReset()));

const summary = {
  summary: { title: 'Kacheem Live', status: 'published', published_revision: 3, capacity: 200, sold: 10, held: 2,
    paid_orders: 4, revenue_minor: 1550000, currency: 'BDT', admitted: 3, failed_delivery: 1 },
  gates: [{ gate: 'North', admitted: 3, refused: 1 }],
};

describe('Overview', () => {
  it('shows sales and gate counts', async () => {
    api.summary.mockResolvedValue(summary);
    render(<Overview me={me('organizer')} />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Kacheem Live' })).toBeInTheDocument();
    expect(screen.getByText('Published, revision 3')).toBeInTheDocument();
    expect(screen.getByText('10 of 200')).toBeInTheDocument();
    expect(screen.getByText('188')).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'North' })).toBeInTheDocument();
  });

  it('handles drafts without gates, errors, gate staff and no event', async () => {
    api.summary.mockResolvedValueOnce({ summary: { ...summary.summary, status: 'draft' }, gates: [] });
    render(<Overview me={me('finance')} />);
    expect(await screen.findByText('Draft, not published')).toBeInTheDocument();
    expect(screen.getByText('No gates yet.')).toBeInTheDocument();
    api.summary.mockRejectedValueOnce(new ApiError(500, 'internal', 'Broken.'));
    render(<Overview me={me('support')} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Broken.');
    render(<Overview me={me('gate')} />);
    expect(screen.getByRole('link', { name: 'Open the scanner' }).getAttribute('href')).toMatch(/^\/scan\/?$/);
    render(<Overview me={{ ...me('organizer'), events: [] }} />);
    expect(screen.getByText('There is no event yet.')).toBeInTheDocument();
  });
});

describe('ReasonAction', () => {
  it('asks for a reason, runs, and reports', async () => {
    const user = userEvent.setup();
    const action = vi.fn().mockRejectedValueOnce(new ApiError(409, 'admitted', 'Already admitted.')).mockResolvedValue({});
    const onDone = vi.fn();
    render(<ReasonAction label="Void" action={action} onDone={onDone} />);
    await user.click(screen.getByRole('button', { name: 'Void' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Void' }));
    await user.type(screen.getByLabelText(/Reason for/), 'lost phone');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Already admitted.');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Void: done.');
    expect(action).toHaveBeenLastCalledWith('lost phone');
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it('runs at once when no reason is needed', async () => {
    const user = userEvent.setup();
    const action = vi.fn().mockResolvedValue({});
    render(<ReasonAction label="Resend" needsReason={false} action={action} onDone={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Resend' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Resend: done.');
  });
});

const detail = {
  order: { public_ref: 'AB12C-DE34F', status: 'paid', purchaser_name: 'Nusrat', email: 'n*****@example.com', phone: '+880*******000',
    total_minor: 310000, currency: 'BDT', created_at: '2027-01-10T04:00:00Z', quantity: 2 },
  tickets: [
    { id: 't1', ordinal: 1, status: 'used', credential_version: 1, gate: 'North', admitted_at: '2027-02-14T12:31:00Z' },
    { id: 't2', ordinal: 2, status: 'valid', credential_version: 2, gate: null, admitted_at: null },
  ],
  attempts: [{ provider: 'bkash', status: 'succeeded', amount_minor: 310000, provider_trx_id: 'TRX1', created_at: '2027-01-10T04:01:00Z' },
    { provider: 'bkash', status: 'failed', amount_minor: 310000, provider_trx_id: null, created_at: '2027-01-10T04:00:30Z' }],
  refunds: [{ reason: 'organizer', status: 'failed', amount_minor: 310000, last_error: 'no balance' },
    { reason: 'late_payment', status: 'completed', amount_minor: 1, last_error: null }],
  history: [{ at: '2027-01-11T00:00:00Z', actor: 'Support', action: 'ticket.reissue', object_id: 't2', reason: 'lost phone' },
    { at: '2027-01-12T00:00:00Z', actor: 'system', action: 'x', object_id: 'o', reason: '' }],
};

describe('Orders', () => {
  it('searches, opens an order and acts on it by role', async () => {
    const user = userEvent.setup();
    api.searchOrders.mockResolvedValue({ orders: [detail.order] });
    api.order.mockResolvedValue(detail);
    api.reissue.mockResolvedValue({});
    api.resend.mockResolvedValue({});
    render(<Orders me={me('support')} />);
    await user.type(screen.getByLabelText(/Reference, email/), 'nusrat');
    await user.click(screen.getByRole('button', { name: 'Search' }));
    await user.click(await screen.findByRole('button', { name: 'AB12C-DE34F' }));
    expect(await screen.findByRole('heading', { name: 'Order AB12C-DE34F' })).toBeInTheDocument();
    expect(screen.getByText(/North, /)).toBeInTheDocument();
    expect(screen.getByText('2 (reissued ×1)')).toBeInTheDocument();
    expect(screen.getByText(/no transaction/)).toBeInTheDocument();
    expect(screen.getByText(/no balance/)).toBeInTheDocument();
    expect(screen.getByText(/“lost phone”/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Refund order' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Resend tickets' }));
    await user.click(screen.getByRole('button', { name: 'Reissue' }));
    await user.type(screen.getByLabelText(/Reason for “Reissue”/), 'lost');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(api.reissue).toHaveBeenCalledWith('t2', 'lost'));
    expect(api.resend).toHaveBeenCalledWith('AB12C-DE34F');
  });

  it('lets finance refund but not change tickets', async () => {
    const user = userEvent.setup();
    api.order.mockResolvedValue({ ...detail, history: [] });
    api.refund.mockResolvedValue({});
    render(<Orders me={me('finance')} initialRef="AB12C-DE34F" />);
    await user.click(await screen.findByRole('button', { name: 'Refund order' }));
    await user.type(screen.getByLabelText(/Reason for/), 'clash');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(api.refund).toHaveBeenCalledWith('AB12C-DE34F', 'clash'));
    expect(screen.queryByRole('button', { name: 'Reissue' })).not.toBeInTheDocument();
    expect(screen.getByText('None.')).toBeInTheDocument();
  });

  it('handles empty results and errors', async () => {
    const user = userEvent.setup();
    api.searchOrders.mockResolvedValueOnce({ orders: [] }).mockRejectedValueOnce(new ApiError(500, 'internal', 'Search failed.'));
    api.order.mockRejectedValue(new ApiError(404, 'order_not_found', 'No such order.'));
    render(<Orders me={me('organizer')} initialRef="NOPE" />);
    expect(await screen.findByRole('alert')).toHaveTextContent('No such order.');
    await user.type(screen.getByLabelText(/Reference, email/), 'x');
    await user.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByText('No orders found.')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Search' }));
    expect(await screen.findByText('Search failed.')).toBeInTheDocument();
  });
});

describe('Finance', () => {
  const ledger = { captured_minor: 2, paid_orders_minor: 1, refunded_minor: 1, refund_open_minor: 0, refund_failed_minor: 0, review_minor: 0 };
  it('shows the ledger and what needs attention', async () => {
    api.finance.mockResolvedValue({ ledger, balanced: true, queue: [
      { kind: 'refund', public_ref: 'A', status: 'failed', amount_minor: 1, detail: 'organizer: x', updated_at: '2027-01-01T00:00:00Z' },
      { kind: 'payment_review', public_ref: 'B', status: 'review', amount_minor: 1, detail: 'bkash T', updated_at: '2027-01-01T00:00:00Z' },
    ] });
    render(<Finance />);
    expect(await screen.findByText('The ledger balances.')).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Refund' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Payment review' })).toBeInTheDocument();
  });

  it('warns when the ledger does not balance, and handles errors', async () => {
    api.finance.mockResolvedValueOnce({ ledger, balanced: false, queue: [] }).mockRejectedValueOnce(new ApiError(403, 'forbidden', 'No.'));
    render(<Finance />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(await screen.findByText(/does not balance/)).toBeInTheDocument();
    expect(screen.getByText('Nothing waiting.')).toBeInTheDocument();
    render(<Finance />);
    expect(await screen.findByRole('alert')).toHaveTextContent('No.');
  });
});

const eventData = {
  event: { title: 'Kacheem Live', organizer: 'Kacheem Comms', venue_name: 'Hall', venue_address: 'Dhaka', description: 'D',
    lineup: ['A', 'B'], starts_at: '2027-02-14T13:30:00Z', doors_at: null, time_zone: 'Asia/Dhaka',
    sale_opens_at: '2027-01-01T00:00:00Z', sale_closes_at: '2027-02-14T12:00:00Z', max_per_order: 6, min_age: 0,
    capacity: 200, status: 'published', published_revision: 2, published_at: '2027-01-01T00:00:00Z' },
  tiers: [{ price_minor: 150000, fee_minor: 5000 }],
};

describe('EventEditor', () => {
  it('saves details, changes capacity and publishes', async () => {
    const user = userEvent.setup();
    api.event.mockResolvedValue(eventData);
    api.updateEvent.mockResolvedValue({});
    api.setCapacity.mockRejectedValueOnce(new ApiError(409, 'below_committed', 'capacity cannot go below 12')).mockResolvedValue({});
    api.publish.mockResolvedValue({ revision: 3 });
    render(<EventEditor me={me('organizer')} />);
    const title = await screen.findByLabelText('Title');
    await user.clear(title);
    await user.type(title, 'Renamed');
    await user.clear(screen.getByLabelText('Lineup (comma separated)'));
    await user.type(screen.getByLabelText('Lineup (comma separated)'), 'X, , Y');
    await user.click(screen.getByRole('button', { name: 'Save details' }));
    expect(await screen.findByText(/Saved\. Publish/)).toBeInTheDocument();
    expect(api.updateEvent.mock.calls[0][1]).toMatchObject({ title: 'Renamed', lineup: ['X', 'Y'], max_per_order: 6, price_minor: 150000 });
    await user.clear(screen.getByLabelText('Capacity'));
    await user.type(screen.getByLabelText('Capacity'), '5');
    await user.click(screen.getByRole('button', { name: 'Change capacity' }));
    expect(await screen.findByText('capacity cannot go below 12')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Change capacity' }));
    expect(await screen.findByText('Capacity updated.')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Publish to the buyer site' }));
    expect(await screen.findByText('Published.')).toBeInTheDocument();
  });

  it('handles drafts, errors and no event', async () => {
    api.event.mockResolvedValueOnce({ ...eventData, event: { ...eventData.event, status: 'draft' } });
    render(<EventEditor me={me('organizer')} />);
    expect(await screen.findByText('Not published')).toBeInTheDocument();
    api.event.mockRejectedValueOnce(new ApiError(500, 'internal', 'Down.'));
    render(<EventEditor me={me('organizer')} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Down.');
    render(<EventEditor me={{ ...me('organizer'), events: [] }} />);
    expect(screen.getByText('There is no event yet.')).toBeInTheDocument();
  });
});

const staffList = { staff: [
  { id: 's1', name: 'Staff One', email: 's@example.org', role: 'organizer', status: 'active' },
  { id: 's2', name: 'Gate Two', email: 'g@example.org', role: 'gate', status: 'active' },
  { id: 's3', name: 'Gone', email: 'x@example.org', role: 'gate', status: 'disabled' },
] };

describe('People', () => {
  it('manages gates, assignments and staff', async () => {
    const user = userEvent.setup();
    api.gates.mockResolvedValue({ gates: [{ id: 'g1', name: 'North', staff: ['Gate Two'] }, { id: 'g2', name: 'South', staff: [] }] });
    api.staff.mockResolvedValue(staffList);
    api.assign.mockRejectedValueOnce(new ApiError(404, 'not_found', 'No such gate.')).mockResolvedValue({});
    api.addGate.mockResolvedValue({ id: 'g3' });
    api.invite.mockRejectedValueOnce(new ApiError(409, 'exists', 'Already has an account.')).mockResolvedValue({ invite_url: 'https://gate.test/invite/#tok' });
    api.disable.mockResolvedValue({});
    render(<People me={me('organizer')} />);
    expect(await screen.findByText(/nobody assigned/)).toBeInTheDocument();
    const south = screen.getByText('South').closest('li');
    await user.click(within(south).getByRole('button', { name: 'Assign' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('No such gate.');
    await user.click(within(south).getByRole('button', { name: 'Assign' }));
    await waitFor(() => expect(api.assign).toHaveBeenLastCalledWith('g2', 's2'));
    await user.type(screen.getByLabelText('New gate'), 'East');
    await user.click(screen.getByRole('button', { name: 'Add gate' }));
    await waitFor(() => expect(api.addGate).toHaveBeenCalledWith('live', 'East'));
    expect(screen.getAllByRole('button', { name: 'Revoke access' })).toHaveLength(1);
    await user.type(screen.getByLabelText('Name'), 'New Person');
    await user.type(screen.getByLabelText('Email'), 'new@example.org');
    await user.selectOptions(screen.getByLabelText('Role'), 'support');
    await user.click(screen.getByRole('button', { name: 'Create invitation' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Already has an account.');
    await user.click(screen.getByRole('button', { name: 'Create invitation' }));
    expect(await screen.findByText('https://gate.test/invite/#tok')).toBeInTheDocument();
    expect(api.invite).toHaveBeenLastCalledWith('new@example.org', 'New Person', 'support');
  });

  it('handles no gate staff, loading and errors', async () => {
    api.gates.mockResolvedValueOnce({ gates: [{ id: 'g1', name: 'North', staff: [] }] });
    api.staff.mockResolvedValueOnce({ staff: [staffList.staff[0]] });
    render(<People me={me('organizer')} />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(await screen.findByText(/nobody assigned/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Assign' })).not.toBeInTheDocument();
    api.gates.mockRejectedValueOnce(new ApiError(500, 'internal', 'Gates down.'));
    api.staff.mockResolvedValueOnce(staffList);
    render(<People me={me('organizer')} />);
    expect(await screen.findByText('Gates down.')).toBeInTheDocument();
    api.gates.mockResolvedValueOnce({ gates: [] });
    api.staff.mockRejectedValueOnce(new ApiError(500, 'internal', 'Staff down.'));
    render(<People me={me('organizer')} />);
    expect(await screen.findByText('Staff down.')).toBeInTheDocument();
  });
});

describe('AuditLog', () => {
  it('lists recent staff actions', async () => {
    api.audit.mockResolvedValueOnce({ entries: [{ at: '2027-01-01T00:00:00Z', actor: 'Org', action: 'event.publish', object_kind: 'event', object_id: '0123456789', reason: '' }] })
      .mockRejectedValueOnce(new ApiError(403, 'forbidden', 'No.'));
    render(<AuditLog />);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(await screen.findByRole('cell', { name: 'event.publish' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'event 01234567' })).toBeInTheDocument();
    render(<AuditLog />);
    expect(await screen.findByRole('alert')).toHaveTextContent('No.');
  });
});

describe('more actions', () => {
  it('voids a ticket from the order', async () => {
    const user = userEvent.setup();
    api.order.mockResolvedValue(detail);
    api.voidTicket.mockResolvedValue({});
    render(<Orders me={me('organizer')} initialRef="AB12C-DE34F" />);
    await user.click(await screen.findByRole('button', { name: 'Void' }));
    await user.type(screen.getByLabelText(/Reason for “Void”/), 'duplicate');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(api.voidTicket).toHaveBeenCalledWith('t2', 'duplicate'));
  });

  it('revokes access and assigns a chosen person', async () => {
    const user = userEvent.setup();
    const more = { staff: [...staffList.staff, { id: 's4', name: 'Gate Four', email: 'f@example.org', role: 'gate', status: 'active' }] };
    api.gates.mockResolvedValue({ gates: [{ id: 'g1', name: 'North', staff: [] }] });
    api.staff.mockResolvedValue(more);
    api.assign.mockResolvedValue({});
    api.disable.mockResolvedValue({});
    render(<People me={me('organizer')} />);
    await user.selectOptions(await screen.findByLabelText('Gate staff'), 's4');
    await user.click(screen.getByRole('button', { name: 'Assign' }));
    await waitFor(() => expect(api.assign).toHaveBeenCalledWith('g1', 's4'));
    await user.click(screen.getAllByRole('button', { name: 'Revoke access' })[0]);
    await user.type(screen.getByLabelText(/Reason for/), 'left the team');
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(api.disable).toHaveBeenCalledWith('s2', 'left the team'));
  });
});
