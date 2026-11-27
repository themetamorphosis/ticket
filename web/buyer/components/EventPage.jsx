'use client';

import { Calendar03Icon, Location01Icon, Ticket01Icon } from '@hugeicons/core-free-icons';
import Link from 'next/link';
import { useEffect, useRef, useState } from 'react';
import { getAvailability, loadEvent } from '../lib/api';
import { formatDateTime, formatMoney, formatTime } from '../lib/format';
import { BookingSheet } from './BookingSheet';
import { Icon } from './Icon';

/** The concert page, rendered in the browser from the published event.json. */
export function EventPage({ onBooked }) {
  const [event, setEvent] = useState(null);
  const [failed, setFailed] = useState(false);
  const [availability, setAvailability] = useState(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  const cta = useRef(null);

  useEffect(() => {
    loadEvent()
      .then((snap) => {
        setEvent(snap.event);
        return getAvailability(snap.event.slug)
          .then(setAvailability)
          .catch(() => setAvailability({ state: 'unknown' }));
      })
      .catch(() => setFailed(true));
  }, []);

  if (failed) {
    return (
      <main className="page">
        <p role="alert">We could not load this event. Please refresh the page.</p>
      </main>
    );
  }
  if (!event) {
    return (
      <main className="page" aria-busy="true">
        <p>Loading event…</p>
      </main>
    );
  }

  const tz = event.time_zone;
  const tt = event.ticket_type;
  function closeSheet() {
    setSheetOpen(false);
    cta.current.focus();
  }

  return (
    <main className="page event">
      <div className="art" aria-hidden="true" />
      <p className="organizer">{event.organizer}</p>
      <h1>{event.title}</h1>
      <ul className="facts">
        <li>
          <Icon icon={Calendar03Icon} />
          <span>
            {formatDateTime(event.starts_at, tz)}
            {event.doors_at && <> · Doors {formatTime(event.doors_at, tz)}</>}
          </span>
        </li>
        <li>
          <Icon icon={Location01Icon} />
          <span>
            {event.venue.name}
            {event.venue.address && <>, {event.venue.address}</>}
          </span>
        </li>
        <li>
          <Icon icon={Ticket01Icon} />
          <span>
            {tt.name}: {formatMoney(tt.price_minor + tt.fee_minor, event.currency)} including{' '}
            {formatMoney(tt.fee_minor, event.currency)} fees
          </span>
        </li>
      </ul>
      {event.description && <p className="description">{event.description}</p>}
      {event.lineup.length > 0 && (
        <section aria-labelledby="lineup">
          <h2 id="lineup">Lineup</h2>
          <ul className="lineup">
            {event.lineup.map((name) => (
              <li key={name}>{name}</li>
            ))}
          </ul>
        </section>
      )}

      <div className="cta-bar">
        <SaleStatus availability={availability} event={event} />
        {availability?.state === 'on_sale' ? (
          <Link
            ref={cta}
            className="cta"
            href="/book/"
            onClick={(e) => {
              e.preventDefault();
              setSheetOpen(true);
            }}
          >
            Get tickets
          </Link>
        ) : (
          <button ref={cta} type="button" className="cta" disabled>
            Get tickets
          </button>
        )}
      </div>

      {sheetOpen && <BookingSheet event={event} onClose={closeSheet} onBooked={onBooked} />}
    </main>
  );
}

const MESSAGES = {
  reserved: 'Every remaining ticket is held by a buyer in checkout. Released tickets reappear here.',
  sold_out: 'Sold out.',
  closed: 'Ticket sales have closed.',
  unknown: 'Ticket availability is unavailable right now. Please refresh in a moment.',
};

function SaleStatus({ availability, event }) {
  let text;
  if (!availability) {
    text = 'Checking availability…';
  } else if (availability.state === 'on_sale') {
    text = null;
  } else if (availability.state === 'not_open') {
    text = `Sales open ${formatDateTime(availability.opens_at, event.time_zone)}.`;
  } else {
    text = MESSAGES[availability.state] ?? MESSAGES.unknown;
  }
  return (
    <p className="status" aria-live="polite">
      {text}
    </p>
  );
}
