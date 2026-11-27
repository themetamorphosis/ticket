'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';
import { createOrder, getQuote } from '../lib/api';
import { formatMoney } from '../lib/format';
import { Field } from './Field';

export function orderPath(ref) {
  return `/order/?ref=${encodeURIComponent(ref)}`;
}

const EMPTY = { quantity: '1', name: '', email: '', phone: '', age: '' };

/**
 * Guest booking: one set of contact details for every place in the order.
 * The total always comes from the server quote. The idempotency key is kept
 * while the details stay the same, so a retry or double submit reuses the
 * same hold; any change starts a new key and therefore a new quote.
 */
export function BookingForm({ event, onBooked }) {
  const router = useRouter();
  const [values, setValues] = useState(EMPTY);
  const [latestQuote, setQuote] = useState(null);
  // Only a quote for the quantity currently selected is shown or submittable.
  const quote = latestQuote?.quantity === Number(values.quantity) ? latestQuote : null;
  const [fieldErrors, setFieldErrors] = useState({});
  const [formError, setFormError] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  const key = useRef(null);

  useEffect(() => {
    let current = true;
    getQuote(event.slug, values.quantity)
      .then((q) => current && setQuote(q))
      .catch(() => current && setQuote(null));
    return () => {
      current = false;
    };
  }, [event.slug, values.quantity]);

  const change = (name) => (e) => {
    setValues((v) => ({ ...v, [name]: e.target.value }));
    setFieldErrors((f) => ({ ...f, [name]: undefined }));
    key.current = null;
  };

  async function submit(e) {
    e.preventDefault();
    setSubmitting(true);
    setFormError(null);
    key.current ??= crypto.randomUUID();
    try {
      const order = await createOrder(key.current, {
        event: event.slug,
        quantity: Number(values.quantity),
        name: values.name,
        email: values.email,
        phone: values.phone,
        age: Number(values.age),
      });
      (onBooked ?? ((o) => router.push(orderPath(o.ref))))(order);
    } catch (err) {
      // The API client only ever throws ApiError. A network error keeps the
      // key, so retrying cannot create a second hold.
      setFieldErrors(err.details.fields ?? {});
      setFormError(err);
    } finally {
      setSubmitting(false);
    }
  }

  const quantities = Array.from({ length: event.max_per_order }, (_, i) => String(i + 1));
  return (
    <form className="booking" onSubmit={submit} noValidate>
      <Field id="quantity" label="Tickets" error={fieldErrors.quantity}>
        {(a11y) => (
          <select {...a11y} name="quantity" value={values.quantity} onChange={change('quantity')}>
            {quantities.map((q) => (
              <option key={q} value={q}>
                {q}
              </option>
            ))}
          </select>
        )}
      </Field>
      <Field id="name" label="Purchaser name" error={fieldErrors.name}>
        {(a11y) => <input {...a11y} name="name" autoComplete="name" value={values.name} onChange={change('name')} />}
      </Field>
      <Field id="email" label="Email" hint="Your ticket PDFs are emailed here." error={fieldErrors.email}>
        {(a11y) => (
          <input {...a11y} name="email" type="email" autoComplete="email" value={values.email} onChange={change('email')} />
        )}
      </Field>
      <Field id="phone" label="Mobile number" hint="We text your ticket link here. Include the country code, for example +880." error={fieldErrors.phone}>
        {(a11y) => (
          <input {...a11y} name="phone" type="tel" autoComplete="tel" value={values.phone} onChange={change('phone')} />
        )}
      </Field>
      <Field id="age" label="Age" error={fieldErrors.age}>
        {(a11y) => (
          <input {...a11y} name="age" type="number" inputMode="numeric" min="0" max="120" value={values.age} onChange={change('age')} />
        )}
      </Field>

      <Summary quote={quote} />

      {formError && <FormError error={formError} />}

      <button type="submit" className="primary" disabled={submitting || !quote}>
        {submitting ? 'Holding your tickets…' : 'Hold tickets'}
      </button>
      <p className="hint">We hold your tickets for a limited time while you complete checkout.</p>
    </form>
  );
}

function Summary({ quote }) {
  if (!quote) {
    return (
      <p className="summary" aria-live="polite">
        Calculating total…
      </p>
    );
  }
  const money = (minor) => formatMoney(minor, quote.currency);
  return (
    <dl className="summary" aria-live="polite">
      <dt>
        {quote.quantity} × {money(quote.unit_price_minor)}
      </dt>
      <dd>{money(quote.subtotal_minor)}</dd>
      <dt>Fees</dt>
      <dd>{money(quote.fees_minor)}</dd>
      <dt>Total ({quote.currency})</dt>
      <dd className="total">{money(quote.total_minor)}</dd>
    </dl>
  );
}

function FormError({ error }) {
  return (
    <div className="form-error" role="alert">
      <p>{error.message}</p>
      {error.code === 'active_hold' && (
        <p>
          <Link href={orderPath(error.details.order_ref)}>View the tickets you are holding</Link>
        </p>
      )}
    </div>
  );
}
