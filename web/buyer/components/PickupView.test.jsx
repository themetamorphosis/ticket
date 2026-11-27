import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, exchangePickup, listPickups } from '../lib/api';
import { tickets } from '../test/fixtures';
import { PickupView } from './PickupView';

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  exchangePickup: vi.fn(),
  listPickups: vi.fn(),
}));

function pickup(ts) {
  return {
    orders: [
      {
        ref: 'AB12C-DE34F',
        status: 'paid',
        event: { title: 'Kacheem Live', starts_at: '2027-02-14T13:30:00Z', time_zone: 'Asia/Dhaka' },
        tickets: ts,
      },
    ],
  };
}

beforeEach(() => {
  exchangePickup.mockReset();
  listPickups.mockReset();
});
afterEach(() => vi.useRealTimers());

describe('PickupView', () => {
  it('exchanges the secret from the link and strips it from the address bar', async () => {
    window.history.pushState({}, '', '/pickup/#s3cret');
    exchangePickup.mockResolvedValue(pickup(tickets(3)));
    render(<PickupView />);
    expect(screen.getByText('Loading your tickets…')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Kacheem Live' })).toBeInTheDocument();
    expect(exchangePickup).toHaveBeenCalledWith('s3cret');
    expect(window.location.hash).toBe('');
    const links = screen.getAllByRole('link', { name: 'Download PDF' });
    expect(links).toHaveLength(3);
    expect(links[2]).toHaveAttribute('href', '/api/v1/tickets/t3/pdf');
    expect(links[2]).toHaveAttribute('download', 'ticket-3-of-3.pdf');
    expect(screen.getByText(/Order AB12C-DE34F/)).toBeInTheDocument();
  });

  it('lists earlier pickups when opened without a secret', async () => {
    window.history.pushState({}, '', '/pickup/');
    listPickups.mockResolvedValue({ orders: [] });
    render(<PickupView />);
    expect(await screen.findByText(/Open the link from your text message/)).toBeInTheDocument();
  });

  it('polls while tickets are being prepared', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    listPickups
      .mockRejectedValueOnce(new ApiError(0, 'network', 'offline'))
      .mockResolvedValueOnce(pickup(tickets(2, true)));
    exchangePickup.mockResolvedValue(pickup(tickets(2, false)));
    render(<PickupView getSecret={() => 's'} pollMs={50} />);
    expect(await screen.findAllByText('Preparing…')).toHaveLength(2);
    await act(async () => vi.advanceTimersByTime(150));
    expect(await screen.findAllByRole('link', { name: 'Download PDF' })).toHaveLength(2);
  });

  it('stops polling after the limit', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    exchangePickup.mockResolvedValue(pickup(tickets(1, false)));
    listPickups.mockResolvedValue(pickup(tickets(1, false)));
    render(<PickupView getSecret={() => 's'} pollMs={20} maxPolls={2} />);
    await screen.findByText('Preparing…');
    await waitFor(() => expect(listPickups).toHaveBeenCalledTimes(2));
    await act(async () => vi.advanceTimersByTimeAsync(200));
    expect(listPickups).toHaveBeenCalledTimes(2);
  });

  it.each([
    [new ApiError(404, 'pickup_invalid', 'x'), 'This ticket link is not valid or has expired.'],
    [new ApiError(0, 'network', 'Check your connection.'), 'Check your connection.'],
  ])('explains a link that cannot be used (%#)', async (err, text) => {
    exchangePickup.mockRejectedValue(err);
    render(<PickupView getSecret={() => 'bad'} />);
    expect(await screen.findByRole('alert')).toHaveTextContent(text);
  });
});
