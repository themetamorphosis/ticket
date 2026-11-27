import { ShieldUserIcon } from '@hugeicons/core-free-icons';
import { Icon } from '../components/Icon';

// Placeholder until the 65–78% milestone adds the dashboard and scanner.
export default function StaffHomePage() {
  return (
    <main className="page">
      <h1>Staff console</h1>
      <p>The organizer dashboard and gate scanner will live here.</p>
      <p className="note">
        <Icon icon={ShieldUserIcon} />
        Reachable only from enrolled devices on the staff VPN.
      </p>
    </main>
  );
}
