import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, getAvailability, getQuote, loadEvent } from '../lib/api';
import { quote, snapshot } from '../test/fixtures';
import { EventPage } from './EventPage';

vi.mock('next/navigation', () => ({ useRouter: () => ({ push: vi.fn() }) }));

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  loadEvent: vi.fn(),
  getAvailability: vi.fn(),
  getQuote: vi.fn(),
}));

function availability(state) {
  return { state, remaining: 10, opens_at: '2027-01-10T04:00:00Z', closes_at: '2027-02-14T12:00:00Z' };
}

beforeEach(() => {
  loadEvent.mockReset().mockResolvedValue(snapshot);
  getAvailability.mockReset().mockResolvedValue(availability('on_sale'));
  getQuote.mockReset().mockResolvedValue(quote(1));
});

describe('EventPage', () => {
  it('renders the published event in its own time zone', async () => {
    render(<EventPage />);
    expect(screen.getByText('Loading event…')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { level: 1, name: 'Kacheem Live' })).toBeInTheDocument();
    expect(screen.getByText(/Sunday,? 14 February 2027.*19:30.*Doors 18:30/)).toBeInTheDocument();
    expect(screen.getByText('Test Hall, 1 Test Road, Dhaka')).toBeInTheDocument();
    expect(screen.getByText(/General admission: .*1,550\.00 including .*50\.00 fees/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Lineup' })).toBeInTheDocument();
    expect(screen.getByText('Artist Two')).toBeInTheDocument();
  });

  it('opens the booking sheet from Get tickets and returns focus on close', async () => {
    const user = userEvent.setup();
    render(<EventPage />);
    const cta = await screen.findByRole('link', { name: 'Get tickets' });
    expect(cta.getAttribute('href')).toMatch(/^\/book\/?$/);
    expect(document.querySelector('.status')).toBeEmptyDOMElement();
    await user.click(cta);
    expect(screen.getByRole('dialog', { name: 'Get tickets' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(cta).toHaveFocus();
  });

  it.each([
    ['not_open', /Sales open Sunday,? 10 January 2027.*10:00/],
    ['reserved', /held by a buyer in checkout/],
    ['sold_out', /Sold out\./],
    ['closed', /Ticket sales have closed\./],
    ['something_new', /availability is unavailable/],
  ])('explains the %s state and disables the button', async (state, text) => {
    getAvailability.mockResolvedValue(availability(state));
    render(<EventPage />);
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Get tickets' })).toBeDisabled();
  });

  it('shows the event even when availability cannot be read', async () => {
    getAvailability.mockRejectedValue(new ApiError(503, 'internal', 'down'));
    render(<EventPage />);
    expect(await screen.findByText(/availability is unavailable/)).toBeInTheDocument();
  });

  it('shows checking while availability loads', async () => {
    getAvailability.mockReturnValue(new Promise(() => {}));
    render(<EventPage />);
    expect(await screen.findByText('Checking availability…')).toBeInTheDocument();
  });

  it('omits optional details that were not published', async () => {
    loadEvent.mockResolvedValue({
      ...snapshot,
      event: { ...snapshot.event, doors_at: null, description: '', lineup: [], venue: { name: 'Test Hall', address: '' } },
    });
    render(<EventPage />);
    expect(await screen.findByText('Test Hall')).toBeInTheDocument();
    expect(screen.queryByText(/Doors/)).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Lineup' })).not.toBeInTheDocument();
    expect(document.querySelector('.description')).toBeNull();
  });

  it('says so when the event cannot be loaded', async () => {
    loadEvent.mockRejectedValue(new ApiError(404, 'unknown', 'missing'));
    render(<EventPage />);
    expect(await screen.findByRole('alert')).toHaveTextContent('We could not load this event.');
  });
});
