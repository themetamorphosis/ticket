import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, getQuote, loadEvent } from '../lib/api';
import { quote, snapshot } from '../test/fixtures';
import { BookPage } from './BookPage';

vi.mock('next/navigation', () => ({ useRouter: () => ({ push: vi.fn() }) }));

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal()),
  loadEvent: vi.fn(),
  getQuote: vi.fn(),
}));

beforeEach(() => getQuote.mockReset().mockResolvedValue(quote(1)));

describe('BookPage', () => {
  it('is a full-page booking form for browsers without the sheet', async () => {
    loadEvent.mockResolvedValue(snapshot);
    render(<BookPage />);
    expect(screen.getByRole('main')).toHaveAttribute('aria-busy', 'true');
    expect(await screen.findByRole('heading', { name: 'Tickets for Kacheem Live' })).toBeInTheDocument();
    expect(screen.getByLabelText('Purchaser name')).toBeInTheDocument();
    expect(screen.getByRole('main')).not.toHaveAttribute('aria-busy');
  });

  it('says so when the event cannot be loaded', async () => {
    loadEvent.mockRejectedValue(new ApiError(404, 'unknown', 'x'));
    render(<BookPage />);
    expect(await screen.findByRole('alert')).toHaveTextContent('could not load this event');
    expect(screen.getByRole('heading', { name: 'Get tickets' })).toBeInTheDocument();
  });
});
