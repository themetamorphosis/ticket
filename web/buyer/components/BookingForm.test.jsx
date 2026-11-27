import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, createOrder, getQuote } from '../lib/api';
import { deferred, order, quote, snapshot } from '../test/fixtures';
import { BookingForm } from './BookingForm';

const push = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ push }) }));

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  getQuote: vi.fn(),
  createOrder: vi.fn(),
}));

const event = snapshot.event;

async function fill(user) {
  await user.type(screen.getByLabelText('Purchaser name'), 'Nusrat Jahan');
  await user.type(screen.getByLabelText('Email'), 'nusrat@example.com');
  await user.type(screen.getByLabelText('Mobile number'), '+8801711000000');
  await user.type(screen.getByLabelText('Age'), '28');
}

beforeEach(() => {
  getQuote.mockReset().mockImplementation((slug, qty) => Promise.resolve(quote(Number(qty))));
  createOrder.mockReset();
});

describe('BookingForm', () => {
  it('shows the server quote and re-quotes when the quantity changes', async () => {
    const user = userEvent.setup();
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    expect(screen.getByText('Calculating total…')).toBeInTheDocument();
    expect(await screen.findByText(/1,550\.00/, { selector: '.total' })).toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(4);
    await user.selectOptions(screen.getByLabelText('Tickets'), '3');
    expect(await screen.findByText(/4,650\.00/, { selector: '.total' })).toBeInTheDocument();
    expect(getQuote).toHaveBeenLastCalledWith('live', '3');
  });

  it('ignores a quote that arrives after the quantity changed', async () => {
    const user = userEvent.setup();
    const slow = deferred();
    const slowFail = deferred();
    getQuote
      .mockImplementationOnce(() => slow.promise)
      .mockImplementationOnce(() => slowFail.promise)
      .mockImplementation((s, q) => Promise.resolve(quote(Number(q))));
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    await user.selectOptions(screen.getByLabelText('Tickets'), '2');
    await user.selectOptions(screen.getByLabelText('Tickets'), '3');
    expect(await screen.findByText(/4,650\.00/, { selector: '.total' })).toBeInTheDocument();
    await act(async () => {
      slow.resolve(quote(1));
      slowFail.reject(new ApiError(500, 'internal', 'x'));
    });
    expect(screen.getByText(/4,650\.00/, { selector: '.total' })).toBeInTheDocument();
  });

  it('keeps the submit disabled while no quote is available', async () => {
    getQuote.mockImplementation(() => Promise.reject(new ApiError(0, 'network', 'offline')));
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    await waitFor(() => expect(getQuote).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'Hold tickets' })).toBeDisabled();
  });

  it('submits the booking once and hands the order on', async () => {
    const user = userEvent.setup();
    const onBooked = vi.fn();
    const pending = deferred();
    createOrder.mockImplementation(() => pending.promise);
    render(<BookingForm event={event} onBooked={onBooked} />);
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    await fill(user);
    await user.click(screen.getByRole('button', { name: 'Hold tickets' }));
    expect(screen.getByRole('button', { name: 'Holding your tickets…' })).toBeDisabled();
    await act(async () => pending.resolve(order()));
    expect(onBooked).toHaveBeenCalledWith(expect.objectContaining({ ref: 'AB12C-DE34F', status: 'held' }));
    const [key, body] = createOrder.mock.calls[0];
    expect(key).toMatch(/^[0-9a-f-]{36}$/);
    expect(body).toEqual({ event: 'live', quantity: 1, name: 'Nusrat Jahan', email: 'nusrat@example.com', phone: '+8801711000000', age: 28 });
  });

  it('reuses the idempotency key on retry and starts a new one after a change', async () => {
    const user = userEvent.setup();
    createOrder.mockImplementation(() => Promise.reject(new ApiError(0, 'network', 'We could not reach the ticket service.')));
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    await fill(user);
    const submit = screen.getByRole('button', { name: 'Hold tickets' });
    await user.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent('We could not reach the ticket service.');
    await user.click(submit);
    await user.type(screen.getByLabelText('Age'), '9');
    await user.click(submit);
    const keys = createOrder.mock.calls.map((c) => c[0]);
    expect(keys[0]).toBe(keys[1]);
    expect(keys[2]).not.toBe(keys[1]);
  });

  it('marks invalid fields and clears each error when it is edited', async () => {
    const user = userEvent.setup();
    createOrder.mockRejectedValue(
      new ApiError(422, 'invalid', 'Check the highlighted details.', { fields: { email: 'Enter a valid email address.' } }),
    );
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    await user.click(screen.getByRole('button', { name: 'Hold tickets' }));
    const email = screen.getByLabelText('Email');
    expect(await screen.findByText('Enter a valid email address.')).toBeInTheDocument();
    expect(email).toHaveAttribute('aria-invalid', 'true');
    expect(email).toHaveAttribute('aria-describedby', 'email-hint email-error');
    expect(screen.getByLabelText('Purchaser name')).not.toHaveAttribute('aria-describedby');
    await user.type(email, 'x');
    expect(email).not.toHaveAttribute('aria-invalid');
    expect(screen.queryByText('Enter a valid email address.')).not.toBeInTheDocument();
  });

  it('links to the hold the buyer already has', async () => {
    const user = userEvent.setup();
    createOrder.mockRejectedValue(
      new ApiError(409, 'active_hold', 'You already have tickets on hold.', { order_ref: 'AB12C-DE34F' }),
    );
    render(<BookingForm event={event} onBooked={vi.fn()} />);
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    await user.click(screen.getByRole('button', { name: 'Hold tickets' }));
    // next/link applies trailingSlash only in the real build.
    const link = await screen.findByRole('link', { name: 'View the tickets you are holding' });
    expect(link.getAttribute('href')).toMatch(/^\/order\/?\?ref=AB12C-DE34F$/);
  });

  it('navigates to the order page by default', async () => {
    const user = userEvent.setup();
    createOrder.mockResolvedValue(order({ ref: 'AB12C DE34F' }));
    render(<BookingForm event={event} />);
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    await user.click(screen.getByRole('button', { name: 'Hold tickets' }));
    expect(push).toHaveBeenCalledWith('/order/?ref=AB12C%20DE34F');
  });
});
