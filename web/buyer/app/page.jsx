import { Ticket01Icon } from '@hugeicons/core-free-icons';
import { Icon } from '../components/Icon';

// Placeholder until the 20–35% milestone renders the published event.json.
export default function HomePage() {
  return (
    <main className="page">
      <h1>Kacheem Comms</h1>
      <p>Concert details will be published here.</p>
      <p className="note">
        <Icon icon={Ticket01Icon} />
        Tickets go on sale once the event is announced.
      </p>
    </main>
  );
}
