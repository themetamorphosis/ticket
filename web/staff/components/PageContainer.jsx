import Box from '@mui/material/Box';

/**
 * The outermost element of every staff page: full width with a fixed gutter
 * (16px below sm, 32px from sm), never centred in a column. `maxWidth` is an
 * opt-in for genuinely narrow content such as a sign-in form.
 */
export function PageContainer({ maxWidth, children }) {
  return (
    <Box
      sx={{
        p: { xs: 2, sm: 4 },
        // Keep clear of the notch in landscape (the viewport extends under it).
        pl: { xs: 'max(16px, env(safe-area-inset-left))', sm: 'max(32px, env(safe-area-inset-left))' },
        pr: { xs: 'max(16px, env(safe-area-inset-right))', sm: 'max(32px, env(safe-area-inset-right))' },
        ...(maxWidth && { maxWidth, mx: 'auto' }),
      }}
    >
      {children}
    </Box>
  );
}
