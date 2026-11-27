'use client';

import { People } from '../../components/People';
import { StaffShell } from '../../components/StaffShell';

export default function PeoplePage() {
  return <StaffShell roles={['organizer']}>{(me) => <People me={me} />}</StaffShell>;
}
