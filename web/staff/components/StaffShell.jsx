'use client';

import Alert from '@mui/material/Alert';
import AppBar from '@mui/material/AppBar';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import List from '@mui/material/List';
import ListItemButton from '@mui/material/ListItemButton';
import ListItemIcon from '@mui/material/ListItemIcon';
import ListItemText from '@mui/material/ListItemText';
import Toolbar from '@mui/material/Toolbar';
import Typography from '@mui/material/Typography';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { api, resetSessionCache } from '../lib/api';
import {
  AccountBalance as AccountBalanceIcon,
  Dashboard as DashboardIcon,
  Event as EventIcon,
  Groups as GroupsIcon,
  History as HistoryIcon,
  Logout as LogoutIcon,
  Menu as MenuIcon,
  QrCodeScanner as QrCodeScannerIcon,
  ReceiptLong as ReceiptLongIcon,
} from './icons';
import { PageContainer } from './PageContainer';
import { PageHeaderSlot } from './PageHeader';
import { SignIn } from './SignIn';
import { LoadingState } from './States';

const DRAWER = 256;

const PAGES = [
  { href: '/', label: 'Overview', icon: DashboardIcon, roles: ['organizer', 'finance', 'support', 'gate'] },
  { href: '/scan/', label: 'Scanner', icon: QrCodeScannerIcon, roles: ['organizer', 'gate'] },
  { href: '/orders/', label: 'Orders', icon: ReceiptLongIcon, roles: ['organizer', 'finance', 'support'] },
  { href: '/finance/', label: 'Finance', icon: AccountBalanceIcon, roles: ['organizer', 'finance'] },
  { href: '/event/', label: 'Event', icon: EventIcon, roles: ['organizer'] },
  { href: '/people/', label: 'Gates and staff', icon: GroupsIcon, roles: ['organizer'] },
  { href: '/audit/', label: 'Audit log', icon: HistoryIcon, roles: ['organizer'] },
];

function Nav({ role, onNavigate }) {
  // With trailingSlash the router may report "/scan" for "/scan/".
  const here = `${usePathname()}/`.replace(/\/+$/, '/');
  return (
    <>
      <Toolbar>
        <Typography variant="h6" component="p" sx={{ fontWeight: 700 }}>
          Kacheem Staff
        </Typography>
      </Toolbar>
      <List aria-label="Staff" sx={{ px: 1.5 }}>
        {PAGES.filter((p) => p.roles.includes(role)).map(({ href, label, icon: PageIcon }) => (
          <ListItemButton key={href} component={Link} href={href} selected={here === href} onClick={onNavigate} sx={{ mb: 0.5 }}>
            <ListItemIcon sx={{ minWidth: 36, color: 'inherit' }}>
              <PageIcon />
            </ListItemIcon>
            <ListItemText primary={label} />
          </ListItemButton>
        ))}
      </List>
    </>
  );
}

/**
 * Loads the signed-in staff member and renders children(me) when their role
 * may use this page, a sign-in form when signed out, or a refusal otherwise.
 * The navigation is a permanent drawer from md up and a temporary one below.
 */
export function StaffShell({ roles, children }) {
  const [me, setMe] = useState(null);
  const [state, setState] = useState('loading');
  const [drawer, setDrawer] = useState(false);
  const [slot, setSlot] = useState(null);

  const load = useCallback(() => {
    api
      .me()
      .then((m) => {
        setMe(m);
        setState('ready');
      })
      .catch((err) => setState(err.code === 'signed_out' ? 'signed_out' : 'error'));
  }, []);
  useEffect(load, [load]);

  async function signOut() {
    await api.logout().catch(() => {});
    resetSessionCache();
    setMe(null);
    setState('signed_out');
  }

  if (state === 'loading') {
    return (
      <PageContainer>
        <LoadingState />
      </PageContainer>
    );
  }
  if (state === 'error') {
    return (
      <PageContainer>
        <Alert severity="error">The staff service is unavailable. Check the VPN and refresh.</Alert>
      </PageContainer>
    );
  }
  if (state === 'signed_out') return <SignIn onSignedIn={load} />;

  const role = me.staff.role;
  const close = () => setDrawer(false);
  return (
    <Box sx={{ display: 'flex', minHeight: '100dvh' }}>
      <AppBar
        position="fixed"
        sx={{
          width: { md: `calc(100% - ${DRAWER}px)` },
          ml: { md: `${DRAWER}px` },
          borderBottom: 1,
          borderColor: 'divider',
          bgcolor: 'background.default',
        }}
      >
        <Toolbar sx={{ gap: 2 }}>
          <IconButton edge="start" aria-label="Open navigation" onClick={() => setDrawer(true)} sx={{ display: { md: 'none' } }}>
            <MenuIcon />
          </IconButton>
          <Box ref={setSlot} sx={{ flex: 1, minWidth: 0, display: 'flex' }} />
          <Box sx={{ display: { xs: 'none', sm: 'block' }, textAlign: 'right' }}>
            <Typography variant="body2" sx={{ fontWeight: 600 }}>
              {me.staff.name}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ textTransform: 'uppercase' }}>
              {role}
            </Typography>
          </Box>
          <Button onClick={signOut} startIcon={<LogoutIcon />}>
            Sign out
          </Button>
        </Toolbar>
      </AppBar>

      <Box component="nav" aria-label="Staff pages" sx={{ width: { md: DRAWER }, flexShrink: { md: 0 } }}>
        <Drawer
          variant="temporary"
          open={drawer}
          onClose={close}
          sx={{ display: { xs: 'block', md: 'none' }, '& .MuiDrawer-paper': { width: DRAWER } }}
        >
          <Nav role={role} onNavigate={close} />
        </Drawer>
        <Drawer
          variant="permanent"
          open
          sx={{ display: { xs: 'none', md: 'block' }, '& .MuiDrawer-paper': { width: DRAWER, borderRight: 0, bgcolor: 'background.default' } }}
        >
          <Nav role={role} />
        </Drawer>
      </Box>

      <Box component="main" sx={{ flex: 1, minWidth: 0 }}>
        <Toolbar />
        <PageHeaderSlot.Provider value={slot}>
          {roles.includes(role) ? (
            children(me)
          ) : (
            <PageContainer>
              <Alert severity="error">Your role cannot use this page.</Alert>
            </PageContainer>
          )}
        </PageHeaderSlot.Provider>
      </Box>
    </Box>
  );
}
