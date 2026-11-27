// One row per ticket: a download link once its PDF is ready.
export function TicketList({ tickets }) {
  return (
    <ul className="tickets">
      {tickets.map((t) => (
        <li key={t.id}>
          <span>
            Ticket {t.number} of {t.of}
          </span>
          {t.ready ? (
            <a href={t.pdf_url} download={`ticket-${t.number}-of-${t.of}.pdf`}>
              Download PDF
            </a>
          ) : (
            <span className="hint">Preparing…</span>
          )}
        </li>
      ))}
    </ul>
  );
}
