'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { loadEvent } from '../lib/api';
import { BookingForm } from './BookingForm';

/** Full-page booking: the fallback link target of the Get tickets button. */
export function BookPage({ onBooked }) {
  const [event, setEvent] = useState(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    loadEvent()
      .then((snap) => setEvent(snap.event))
      .catch(() => setFailed(true));
  }, []);

  return (
    <main className="page" aria-busy={event || failed ? undefined : 'true'}>
      <p>
        <Link href="/">Back to the event</Link>
      </p>
      <h1>{event ? `Tickets for ${event.title}` : 'Get tickets'}</h1>
      {failed && <p role="alert">We could not load this event. Please refresh the page.</p>}
      {event && <BookingForm event={event} onBooked={onBooked} />}
    </main>
  );
}
