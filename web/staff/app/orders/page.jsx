'use client';

import { Orders } from '../../components/Orders';
import { StaffShell } from '../../components/StaffShell';

export default function OrdersPage() {
  return <StaffShell roles={['organizer', 'finance', 'support']}>{(me) => <Orders me={me} />}</StaffShell>;
}
