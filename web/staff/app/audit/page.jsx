'use client';

import { AuditLog } from '../../components/AuditLog';
import { StaffShell } from '../../components/StaffShell';

export default function AuditPage() {
  return <StaffShell roles={['organizer']}>{() => <AuditLog />}</StaffShell>;
}
