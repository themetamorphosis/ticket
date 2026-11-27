'use client';

import { Cancel01Icon } from '@hugeicons/core-free-icons';
import { useEffect, useRef } from 'react';
import { BookingForm } from './BookingForm';
import { Icon } from './Icon';

const FOCUSABLE = 'a[href], button:not([disabled]), input, select, textarea';

/**
 * Accessible booking sheet: modal, labelled, Escape closes it, Tab stays
 * inside, and focus starts on the first field. The page passes onClose and
 * returns focus to the Get tickets button. /book/ is the full-page fallback.
 */
export function BookingSheet({ event, onClose, onBooked }) {
  const sheet = useRef(null);

  useEffect(() => {
    sheet.current.querySelector('select').focus();
  }, []);

  function keyDown(e) {
    if (e.key === 'Escape') {
      onClose();
      return;
    }
    if (e.key !== 'Tab') return;
    const items = [...sheet.current.querySelectorAll(FOCUSABLE)];
    const first = items[0];
    const last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  return (
    <div className="backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="sheet" role="dialog" aria-modal="true" aria-labelledby="sheet-title" ref={sheet} onKeyDown={keyDown}>
        <div className="sheet-head">
          <h2 id="sheet-title">Get tickets</h2>
          <button type="button" className="icon-button" aria-label="Close" onClick={onClose}>
            <Icon icon={Cancel01Icon} />
          </button>
        </div>
        <BookingForm event={event} onBooked={onBooked} />
      </div>
    </div>
  );
}
