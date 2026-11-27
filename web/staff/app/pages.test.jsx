import { describe, expect, it } from 'vitest';
import { InviteAccept } from '../components/InviteAccept';
import { StaffShell } from '../components/StaffShell';
import AuditPage from './audit/page';
import EventPage from './event/page';
import FinancePage from './finance/page';
import InvitePage from './invite/page';
import OrdersPage from './orders/page';
import Home from './page';
import PeoplePage from './people/page';
import Scan from './scan/page';
import SellPage from './sell/page';

const m = { staff: { role: 'organizer' }, events: [] };

describe('routes', () => {
  it.each([
    [Home, ['organizer', 'finance', 'support', 'gate']],
    [Scan, ['organizer', 'gate']],
    [OrdersPage, ['organizer', 'finance', 'support']],
    [SellPage, ['organizer', 'finance', 'support']],
    [FinancePage, ['organizer', 'finance']],
    [EventPage, ['organizer']],
    [PeoplePage, ['organizer']],
    [AuditPage, ['organizer']],
  ])('%o renders its screen inside the staff shell for its roles', (Page, roles) => {
    const el = Page();
    expect(el.type).toBe(StaffShell);
    expect(el.props.roles).toEqual(roles);
    expect(el.props.children(m)).toBeTruthy();
  });

  it('serves the invitation page without the shell', () => {
    expect(InvitePage().type).toBe(InviteAccept);
  });
});
