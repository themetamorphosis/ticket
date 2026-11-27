-- Development seed: one draft event with 200 places split across 16 shards.
-- Apply with `make seed`, then `make publish-dev` to publish it.
-- Content is placeholder only; real event details and artwork rights come
-- from the organizer before publication.
BEGIN;

INSERT INTO event (slug, title, organizer, venue_name, venue_address, description, lineup,
    starts_at, doors_at, time_zone, currency, capacity, max_per_order, min_age, hold_minutes,
    sale_opens_at, sale_closes_at)
VALUES ('kacheem-live', 'Kacheem Live (development event)', 'Kacheem Comms',
    'Development Hall', 'Dhaka', 'Placeholder event for local development.',
    ARRAY['Artist One', 'Artist Two', 'Artist Three'],
    now() + interval '60 days', now() + interval '60 days' - interval '90 minutes',
    'Asia/Dhaka', 'BDT', 200, 6, 0, 10,
    now() - interval '1 hour', now() + interval '59 days')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO ticket_type (event_id, name, price_minor, fee_minor, capacity)
SELECT id, 'General admission', 150000, 5000, 200 FROM event WHERE slug = 'kacheem-live'
ON CONFLICT (event_id, name) DO NOTHING;

-- 200 / 16 = 12 remainder 8: the first 8 shards get 13 places.
INSERT INTO inventory_shard (ticket_type_id, shard, capacity)
SELECT t.id, s, 12 + CASE WHEN s < 8 THEN 1 ELSE 0 END
FROM ticket_type t JOIN event e ON e.id = t.event_id, generate_series(0, 15) AS s
WHERE e.slug = 'kacheem-live'
ON CONFLICT DO NOTHING;

COMMIT;
