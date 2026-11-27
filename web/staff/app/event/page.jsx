'use client';

import { EventEditor } from '../../components/EventEditor';
import { StaffShell } from '../../components/StaffShell';

export default function EventPage() {
  return <StaffShell roles={['organizer']}>{(me) => <EventEditor me={me} />}</StaffShell>;
}
