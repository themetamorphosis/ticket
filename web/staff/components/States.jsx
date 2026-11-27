import CircularProgress from '@mui/material/CircularProgress';
import Paper from '@mui/material/Paper';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import { Inbox as InboxIcon } from './icons';

/** A page or panel still fetching. */
export function LoadingState({ label = 'Loading…' }) {
  return (
    <Stack direction="row" aria-busy="true" sx={{ alignItems: 'center', gap: 2, py: 4, color: 'text.secondary' }}>
      <CircularProgress size={20} />
      <Typography>{label}</Typography>
    </Stack>
  );
}

/** A list or panel that resolved to nothing. Not for inside a table. */
export function EmptyState({ title }) {
  return (
    <Paper variant="outlined" sx={{ p: 3, display: 'flex', alignItems: 'center', gap: 2, color: 'text.secondary', borderStyle: 'dashed' }}>
      <InboxIcon size={28} />
      <Typography color="text.primary">{title}</Typography>
    </Paper>
  );
}
