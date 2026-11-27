'use client';

import { Finance } from '../../components/Finance';
import { StaffShell } from '../../components/StaffShell';

export default function FinancePage() {
  return <StaffShell roles={['organizer', 'finance']}>{() => <Finance />}</StaffShell>;
}
