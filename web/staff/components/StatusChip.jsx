import Chip from '@mui/material/Chip';

// Status strings from the staff API mapped onto the semantic palette.
// Anything unlisted is neutral.
const TONE = {
  active: 'success',
  admitted: 'success',
  completed: 'success',
  paid: 'success',
  published: 'success',
  succeeded: 'success',
  valid: 'success',
  invited: 'warning',
  open: 'warning',
  pending: 'warning',
  review: 'warning',
  cancelled: 'error',
  failed: 'error',
  void: 'error',
};

/** A status pill: the raw status, made readable, in its semantic colour. */
export function StatusChip({ status }) {
  return <Chip size="small" label={status.replaceAll('_', ' ')} color={TONE[status] ?? 'default'} />;
}
