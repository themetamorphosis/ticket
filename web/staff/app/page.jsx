'use client';

import { Overview } from '../components/Overview';
import { StaffShell } from '../components/StaffShell';

export default function Home() {
  return <StaffShell roles={['organizer', 'finance', 'support', 'gate']}>{(me) => <Overview me={me} />}</StaffShell>;
}
