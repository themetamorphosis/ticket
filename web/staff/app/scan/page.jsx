'use client';

import { Scanner } from '../../components/Scanner';
import { StaffShell } from '../../components/StaffShell';

export default function Scan() {
  return <StaffShell roles={['organizer', 'gate']}>{(me) => <Scanner me={me} />}</StaffShell>;
}
