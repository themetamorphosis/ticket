import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, cancelOrder, getOrder, resendTickets, startCheckout } from '../lib/api';
import { deferred, order, tickets } from '../test/fixtures';
import { OrderView } from './OrderView';

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  getOrder: vi.fn(),
  cancelOrder: vi.fn(),
  startCheckout: vi.fn(),
  resendTickets: vi.fn(),
}));

const ref = () => 'AB12C-DE34F';
const at = '2027-02-14T04:00:00Z';

beforeEach(() => {
  getOrder.mockReset();
  cancelOrder.mockReset();
  startCheckout.mockReset();
  resendTickets.mockReset();
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

  it('shows the held order with a server-clock countdown and its history', async () => {
    getOrder.mockResolvedValue(order());
    render(<OrderView getRef={ref} />);
    expect(screen.getByText('Loading your order…')).toBeInTheDocument();
    expect(await screen.findByRole('timer')).toHaveTextContent(/held for 10:00, until \d\d:\d\d.*Complete payment/);
    expect(screen.getByText(/Kacheem Live, Sunday,? 14 February 2027/)).toBeInTheDocument();
    expect(screen.getByText(/3 × .*1,550\.00/)).toBeInTheDocument();
    expect(screen.getByText('Nusrat Jahan, nusrat@example.com, +8801711000000')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Pay .*4,650\.00/ })).toBeEnabled();
    expect(screen.getByText(/on the test gateway's secure page/)).toBeInTheDocument();
    expect(screen.getByRole('list')).toHaveTextContent('Tickets held');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('sends the buyer to the payment page', async () => {
    const user = userEvent.setup();
    const navigate = vi.fn();
    const pending = deferred();
    startCheckout.mockReturnValue(pending.promise);
    getOrder.mockResolvedValue(order());
    render(<OrderView getRef={ref} navigate={navigate} />);
    await user.click(await screen.findByRole('button', { name: /^Pay / }));
    expect(screen.getByRole('button', { name: 'Opening payment…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Release these tickets' })).toBeDisabled();
    await act(async () => pending.resolve('https://pay.example/session/1'));
    expect(startCheckout).toHaveBeenCalledWith('AB12C-DE34F');
    expect(navigate).toHaveBeenCalledWith('https://pay.example/session/1');
  });

  it('navigates with the browser by default', async () => {
    const user = userEvent.setup();
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    startCheckout.mockResolvedValue('http://localhost:3000/#paid');
    getOrder.mockResolvedValue(order());
    render(<OrderView getRef={ref} />);
    await user.click(await screen.findByRole('button', { name: /^Pay / }));
    expect(startCheckout).toHaveBeenCalled();
    errors.mockRestore();
  });

  it('explains a payment that could not start and allows another try', async () => {
    const user = userEvent.setup();
    startCheckout.mockRejectedValue(new ApiError(503, 'payment_unavailable', 'Payment is unavailable right now.'));
    getOrder.mockResolvedValue(order({ payment_status: 'failed', payment_provider: 'bkash' }));
    render(<OrderView getRef={ref} />);
    expect(await screen.findByRole('status')).toHaveTextContent('no money was taken');
    expect(screen.getByText(/on bKash's secure page/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /^Try paying again/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Payment is unavailable right now.');
    expect(screen.getByRole('button', { name: /^Try paying again/ })).toBeEnabled();
  });

  it('names PayStation as the payment page', async () => {
    getOrder.mockResolvedValue(order({ payment_provider: 'paystation' }));
    render(<OrderView getRef={ref} />);
    expect(await screen.findByText(/on PayStation's secure page/)).toBeInTheDocument();
  });

  it('polls while the payment is being confirmed, then shows the result', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getOrder
      .mockResolvedValueOnce(order({ payment_status: 'pending' }))
      .mockRejectedValueOnce(new ApiError(0, 'network', 'offline'))
      .mockResolvedValueOnce(order({ status: 'paid', payment_status: 'succeeded' }));
    render(<OrderView getRef={ref} pollMs={100} />);
    expect(await screen.findByText('Checking your payment…')).toBeInTheDocument();
    await act(async () => vi.advanceTimersByTime(250));
    expect(await screen.findByText(/Payment received/)).toBeInTheDocument();
    expect(getOrder).toHaveBeenCalledTimes(3);
  });

  it('stops polling and says so when confirmation takes long', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getOrder.mockResolvedValue(order({ payment_status: 'pending', payment_provider: 'other' }));
    render(<OrderView getRef={ref} pollMs={50} maxPolls={2} />);
    await screen.findByText('Checking your payment…');
    await act(async () => vi.advanceTimersByTime(200));
    expect(await screen.findByText(/still confirming your payment with the payment provider/)).toBeInTheDocument();
    expect(getOrder).toHaveBeenCalledTimes(3);
  });

  it('marks the hold expired when the countdown reaches zero', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const now = Date.now();
    getOrder.mockResolvedValue(
      order({ server_time: new Date(now).toISOString(), expires_at: new Date(now + 2000).toISOString() }),
    );
    render(<OrderView getRef={ref} />);
    expect(await screen.findByRole('timer')).toHaveTextContent(/held for 0:0[12]/);
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
    await act(async () =>
      pending.resolve(order({ status: 'cancelled', timeline: [{ at, kind: 'order_cancelled' }, { at, kind: 'something_new' }] })),
    );
    expect(screen.getByRole('status')).toHaveTextContent('You released these tickets.');
    expect(screen.getByRole('list')).toHaveTextContent(/Tickets released.*something_new/);
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
    [{ status: 'expired' }, 'This hold has expired'],
    [{ status: 'paid', payment_status: 'succeeded' }, 'Payment received.'],
    [{ status: 'refunded', payment_status: 'succeeded' }, 'This order was refunded'],
    [{ status: 'held', payment_status: 'review' }, 'Our support team will contact you'],
    [{ status: 'expired', timeline: [{ at, kind: 'refund_requested' }] }, 'will be refunded in full'],
    [
      { status: 'expired', timeline: [{ at, kind: 'refund_requested' }, { at, kind: 'refund_completed' }] },
      'has been refunded in full',
    ],
  ])('describes the order %#', async (overrides, text) => {
    getOrder.mockResolvedValue(order(overrides));
    render(<OrderView getRef={ref} />);
    await screen.findByRole('heading', { name: /Your order/ });
    expect(screen.getByText(new RegExp(text))).toBeInTheDocument();
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

  it('lists the tickets of a paid order with where they were sent', async () => {
    getOrder.mockResolvedValue(
      order({ status: 'paid', payment_status: 'succeeded', tickets: tickets(3), deliveries: { email: { status: 'sent' }, sms: { status: 'failed' } } }),
    );
    render(<OrderView getRef={ref} />);
    expect(await screen.findAllByRole('link', { name: 'Download PDF' })).toHaveLength(3);
    expect(screen.getByText('Emailed to nusrat@example.com.')).toBeInTheDocument();
    expect(screen.getByText(/could not text \+8801711000000 yet/)).toBeInTheDocument();
  });

  it('polls until every ticket is ready, showing deliveries in progress', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getOrder
      .mockResolvedValueOnce(order({ status: 'paid', payment_status: 'succeeded', tickets: tickets(2, false) }))
      .mockResolvedValueOnce(
        order({ status: 'paid', payment_status: 'succeeded', tickets: tickets(2), deliveries: { sms: { status: 'sent' }, email: { status: 'failed' } } }),
      );
    render(<OrderView getRef={ref} pollMs={50} />);
    expect(await screen.findAllByText('Preparing…')).toHaveLength(2);
    expect(screen.getByText(/Emailing them to nusrat@example.com/)).toBeInTheDocument();
    expect(screen.getByText(/Texting a download link/)).toBeInTheDocument();
    await act(async () => vi.advanceTimersByTime(120));
    expect(await screen.findAllByRole('link', { name: 'Download PDF' })).toHaveLength(2);
    expect(screen.getByText(/A download link was texted/)).toBeInTheDocument();
    expect(screen.getByText(/could not email/)).toBeInTheDocument();
  });

  it('sends the tickets again on request', async () => {
    const user = userEvent.setup();
    const pending = deferred();
    resendTickets.mockReturnValue(pending.promise);
    getOrder.mockResolvedValue(order({ status: 'paid', payment_status: 'succeeded', tickets: tickets(1) }));
    render(<OrderView getRef={ref} />);
    await user.click(await screen.findByRole('button', { name: 'Send my tickets again' }));
    expect(screen.getByRole('button', { name: 'Sending…' })).toBeDisabled();
    await act(async () => pending.resolve({ status: 'queued' }));
    expect(screen.getByText('Your tickets are on their way again.')).toBeInTheDocument();
    expect(resendTickets).toHaveBeenCalledWith('AB12C-DE34F');
  });

  it('explains a resend that came too soon', async () => {
    const user = userEvent.setup();
    resendTickets.mockRejectedValue(new ApiError(429, 'too_soon', 'Your tickets were sent a few minutes ago.'));
    getOrder.mockResolvedValue(order({ status: 'paid', payment_status: 'succeeded', tickets: tickets(1) }));
    render(<OrderView getRef={ref} />);
    await user.click(await screen.findByRole('button', { name: 'Send my tickets again' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('sent a few minutes ago');
    expect(screen.getByRole('button', { name: 'Send my tickets again' })).toBeEnabled();
  });
});
