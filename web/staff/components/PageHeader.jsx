'use client';

import Box from '@mui/material/Box';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import { createContext, useContext } from 'react';
import { createPortal } from 'react-dom';

/**
 * The app bar's header slot: the DOM node PageHeader portals into. It holds
 * the target node only, never the header itself (see DESIGN_GUIDELINES §6).
 */
export const PageHeaderSlot = createContext(null);

function Header({ title, subtitle, actions, compact }) {
  return (
    <Stack direction="row" sx={{ alignItems: 'center', gap: 2, flexWrap: 'wrap', minWidth: 0, flex: 1 }}>
      <Box sx={{ minWidth: 0, flex: 1 }}>
        <Typography component="h1" variant={compact ? 'h6' : 'h5'} noWrap={compact} sx={{ fontWeight: 600 }}>
          {title}
        </Typography>
        {subtitle && (
          <Typography variant="body2" color="text.secondary" noWrap={compact}>
            {subtitle}
          </Typography>
        )}
      </Box>
      {actions && <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap' }}>{actions}</Stack>}
    </Stack>
  );
}

/**
 * Page title, optional subtitle and action buttons. From md up it renders in
 * the top app bar; below md, and outside the shell, it renders in place.
 * `actions` is mounted twice when a slot exists: keep it to buttons.
 */
export function PageHeader(props) {
  const slot = useContext(PageHeaderSlot);
  return (
    <>
      {slot &&
        createPortal(
          <Box sx={{ display: { xs: 'none', md: 'flex' }, flex: 1, minWidth: 0 }}>
            <Header {...props} compact />
          </Box>,
          slot,
        )}
      <Box sx={{ display: { xs: 'flex', md: slot ? 'none' : 'flex' }, mb: 3 }}>
        <Header {...props} />
      </Box>
    </>
  );
}
