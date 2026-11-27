import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../lib/api';
import { me } from '../test/apiMock';
import { render } from '../test/render';
import { OfflineSale } from './OfflineSale';

vi.mock('../lib/api', async (importOriginal) => (await import('../test/apiMock')).apiMock(await importOriginal()));

beforeEach(() => Object.values(api).forEach((f) => f.mockReset()));

const order = { ref: 'AB12C-DE34F', status: 'paid', channel: 'offline', quantity: 2, total_minor: 300000, currency: 'BDT',
  purchaser: { name: 'Rahim Uddin', email: 'rahim@example.com', phone: '+8801811000000', age: 34 } };

async function fill(user) {
  await user.type(screen.getByLabelText(/Buyer name/), 'Rahim Uddin');
  await user.type(screen.getByLabelText(/Mobile number/), '+8801811000000');
  await user.type(screen.getByLabelText(/Email/), 'rahim@example.com');
  await user.type(screen.getByLabelText(/Age/), '34');
  const qty = screen.getByLabelText(/Tickets/);
  await user.clear(qty);
  await user.type(qty, '2');
}

describe('OfflineSale', () => {
  it('records a sale with the amount to collect and sends the tickets', async () => {
    const user = userEvent.setup();
    api.sellOffline.mockResolvedValue(order);
    render(<OfflineSale me={me('support')} />);
    expect(screen.getByText(/1,500.00 per ticket, no online fee/)).toBeInTheDocument();
    await fill(user);
    expect(screen.getByText(/Collect .*3,000.00/)).toBeInTheDocument();
    await user.click(screen.getByLabelText('bKash'));
    await user.type(screen.getByLabelText(/Payment reference/), '8AB12CD34E');
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));

    expect(await screen.findByRole('heading', { name: 'Sale recorded: order AB12C-DE34F' })).toBeInTheDocument();
    expect(screen.getByText(/2 tickets · .*3,000.00 by bKash/)).toBeInTheDocument();
    expect(screen.getByText(/emailed to rahim@example.com.*texted to \+8801811000000/)).toBeInTheDocument();
    const [slug, sale, key] = api.sellOffline.mock.calls[0];
    expect(slug).toBe('live');
    expect(sale).toEqual({ quantity: 2, name: 'Rahim Uddin', email: 'rahim@example.com', phone: '+8801811000000', age: 34,
      method: 'bkash', reference: '8AB12CD34E' });
    expect(key).toMatch(/^offline-[0-9a-f-]{36}$/);

    // The next sale starts empty with a new key.
    api.sellOffline.mockResolvedValue({ ...order, quantity: 1, total_minor: 150000 });
    await user.click(screen.getByRole('button', { name: 'Record another sale' }));
    expect(screen.getByLabelText(/Buyer name/)).toHaveValue('');
    await fill(user);
    const qty = screen.getByLabelText(/Tickets/);
    await user.clear(qty);
    await user.type(qty, '1');
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    expect(await screen.findByText(/1 ticket · .*1,500.00 by Cash/)).toBeInTheDocument();
    expect(api.sellOffline.mock.calls[1][2]).not.toBe(key);
  });

  it('shows field errors and keeps the key for a corrected retry', async () => {
    const user = userEvent.setup();
    api.sellOffline
      .mockRejectedValueOnce(new ApiError(422, 'invalid', 'Check the highlighted details.',
        { fields: { name: 'Enter the purchaser’s name.', method: 'Choose cash, bank or bKash.', quantity: 'Too many.',
          reference: 'Use at most 64 characters.' } }))
      .mockRejectedValueOnce(new ApiError(0, 'network', 'No connection to the server.'))
      .mockResolvedValue(order);
    render(<OfflineSale me={me('finance')} />);
    await user.clear(screen.getByLabelText(/Tickets/));
    expect(screen.getByText(/Collect .*0.00/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    expect(await screen.findByText('Check the highlighted details.')).toBeInTheDocument();
    for (const text of ['Enter the purchaser’s name.', 'Choose cash, bank or bKash.', 'Too many.', 'Use at most 64 characters.']) {
      expect(screen.getByText(text)).toBeInTheDocument();
    }
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    expect(await screen.findByText('No connection to the server.')).toBeInTheDocument();
    expect(screen.queryByText('Too many.')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    await screen.findByRole('heading', { name: /Sale recorded/ });
    const keys = api.sellOffline.mock.calls.map((c) => c[2]);
    expect(new Set(keys).size).toBe(1);
  });

  it('warns and starts a new key when changed details meet an earlier attempt', async () => {
    const user = userEvent.setup();
    api.sellOffline
      .mockRejectedValueOnce(new ApiError(422, 'idempotency_key_reused', 'Changed.'))
      .mockResolvedValue(order);
    render(<OfflineSale me={me('organizer')} />);
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/may already be recorded\. Search Orders/);
    await user.click(screen.getByRole('button', { name: 'Record sale and send tickets' }));
    await waitFor(() => expect(api.sellOffline).toHaveBeenCalledTimes(2));
    expect(api.sellOffline.mock.calls[1][2]).not.toBe(api.sellOffline.mock.calls[0][2]);
  });

  it('says when there is no event', () => {
    render(<OfflineSale me={{ ...me('support'), events: [] }} />);
    expect(screen.getByText('There is no event yet.')).toBeInTheDocument();
  });
});
