import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getQuote } from '../lib/api';
import { quote, snapshot } from '../test/fixtures';
import { BookingSheet } from './BookingSheet';

vi.mock('next/navigation', () => ({ useRouter: () => ({ push: vi.fn() }) }));

vi.mock('../lib/api', async (importOriginal) => ({ ...(await importOriginal()), getQuote: vi.fn() }));

beforeEach(() => getQuote.mockReset().mockResolvedValue(quote(1)));

function renderSheet() {
  const onClose = vi.fn();
  render(<BookingSheet event={snapshot.event} onClose={onClose} onBooked={vi.fn()} />);
  return onClose;
}

describe('BookingSheet', () => {
  it('is a labelled modal that starts on the first field', async () => {
    renderSheet();
    const dialog = screen.getByRole('dialog', { name: 'Get tickets' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByLabelText('Tickets')).toHaveFocus();
    await screen.findByText(/1,550\.00/, { selector: '.total' });
  });

  it('closes on Escape, the close button and a backdrop click only', async () => {
    const user = userEvent.setup();
    const onClose = renderSheet();
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'Close' }));
    await user.click(screen.getByRole('dialog'));
    expect(onClose).toHaveBeenCalledTimes(2);
    fireEvent.click(document.querySelector('.backdrop'));
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it('keeps Tab inside the sheet', async () => {
    renderSheet();
    await screen.findByText(/1,550\.00/, { selector: '.total' });
    const dialog = screen.getByRole('dialog');
    const close = screen.getByRole('button', { name: 'Close' });
    const submit = screen.getByRole('button', { name: 'Hold tickets' });

    close.focus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(submit).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(close).toHaveFocus();

    // In the middle, Tab is left to the browser.
    const name = screen.getByLabelText('Purchaser name');
    name.focus();
    expect(fireEvent.keyDown(dialog, { key: 'Tab' })).toBe(true);
    expect(fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true })).toBe(true);
    expect(fireEvent.keyDown(dialog, { key: 'a' })).toBe(true);
    expect(name).toHaveFocus();
  });
});
