'use client';

import { OfflineSale } from '../../components/OfflineSale';
import { StaffShell } from '../../components/StaffShell';

export default function SellPage() {
  return <StaffShell roles={['organizer', 'finance', 'support']}>{(me) => <OfflineSale me={me} />}</StaffShell>;
}
