import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, cancelOrder, getOrder } from '../lib/api';
import { deferred, order } from '../test/fixtures';
import { OrderView } from './OrderView';

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  getOrder: vi.fn(),
  cancelOrder: vi.fn(),
}));

const ref = () => 'AB12C-DE34F';

beforeEach(() => {
  getOrder.mockReset();
  cancelOrder.mockReset();
});
afterEach(() => vi.useRealTimers());

describe('OrderView', () => {
  it('reads the reference from the URL by default', async () => {
    window.history.pushState({}, '', '/order/?ref=FROMURL01');
    getOrder.mockResolvedValue(order());
    render(<OrderView />);
    await screen.findByRole('heading', { name: /Your order AB12C-DE34F/ });
    expect(getOrder).toHaveBeenCalledWith('FROMURL01');
  });

  it('shows the held order with a server-clock countdown', async () => {
    getOrder.mockResolvedValue(order());
    render(<OrderView getRef={ref} />);
    expect(screen.getByText('Loading your order…')).toBeInTheDocument();
    expect(await screen.findByRole('status')).toHaveTextContent(/held for 10:00, until \d\d:\d\d/);
    expect(screen.getByText(/Kacheem Live, Sunday,? 14 February 2027/)).toBeInTheDocument();
    expect(screen.getByText(/3 × .*1,550\.00/)).toBeInTheDocument();
    expect(screen.getByText('Nusrat Jahan, nusrat@example.com, +8801711000000')).toBeInTheDocument();
  });

  it('marks the hold expired when the countdown reaches zero', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const now = Date.now();
    getOrder.mockResolvedValue(
      order({ server_time: new Date(now).toISOString(), expires_at: new Date(now + 2000).toISOString() }),
    );
    render(<OrderView getRef={ref} />);
    expect(await screen.findByRole('status')).toHaveTextContent(/held for 0:0[12]/);
    await act(async () => vi.advanceTimersByTime(3000));
    expect(screen.getByRole('status')).toHaveTextContent('This hold has expired');
    expect(screen.queryByRole('button', { name: /Release/ })).not.toBeInTheDocument();
  });

  it('releases the tickets on request', async () => {
    const user = userEvent.setup();
    getOrder.mockResolvedValue(order());
    const pending = deferred();
    cancelOrder.mockReturnValue(pending.promise);
    render(<OrderView getRef={ref} />);
    await user.click(await screen.findByRole('button', { name: 'Release these tickets' }));
    expect(screen.getByRole('button', { name: 'Releasing…' })).toBeDisabled();
    await act(async () => pending.resolve(order({ status: 'cancelled' })));
    expect(screen.getByRole('status')).toHaveTextContent('You released these tickets.');
    expect(cancelOrder).toHaveBeenCalledWith('AB12C-DE34F');
  });

  it('reports a failed release and keeps the order', async () => {
    const user = userEvent.setup();
    getOrder.mockResolvedValue(order());
    cancelOrder.mockRejectedValue(new ApiError(409, 'not_held', 'This order is no longer on hold.'));
    render(<OrderView getRef={ref} />);
    await user.click(await screen.findByRole('button', { name: 'Release these tickets' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('This order is no longer on hold.');
    expect(screen.getByRole('button', { name: 'Release these tickets' })).toBeEnabled();
  });

  it.each([
    ['expired', 'This hold has expired'],
    ['paid', 'Payment received.'],
  ])('describes a %s order', async (status, text) => {
    getOrder.mockResolvedValue(order({ status }));
    render(<OrderView getRef={ref} />);
    expect(await screen.findByRole('status')).toHaveTextContent(text);
  });

  it.each([
    [() => null, null, 'missing its order reference'],
    [ref, new ApiError(404, 'order_not_found', 'No such order.'), 'could not find this order in this browser'],
    [ref, new ApiError(0, 'network', 'Check your connection.'), 'Check your connection.'],
  ])('explains why the order cannot be shown (%#)', async (getRef, err, text) => {
    getOrder.mockRejectedValue(err);
    render(<OrderView getRef={getRef} />);
    expect(await screen.findByRole('alert')).toHaveTextContent(text);
    expect(screen.getByRole('link', { name: 'Back to the event' })).toHaveAttribute('href', '/');
  });
});
