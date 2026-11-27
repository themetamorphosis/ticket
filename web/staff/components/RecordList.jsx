import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';

// Wide tables: render a table from md up and cards below it, and let CSS pick
// one (DESIGN_GUIDELINES §7). No JS media query, so nothing flashes.

export function DesktopOnly({ children }) {
  return <Box sx={{ display: { xs: 'none', md: 'block' } }}>{children}</Box>;
}

export function MobileOnly({ children }) {
  return <Stack spacing={1.5} sx={{ display: { xs: 'flex', md: 'none' } }}>{children}</Stack>;
}

/**
 * One record as a card. It leads with what the record is identified by
 * (`title`, with `badge` beside it) and lists only the fields that still mean
 * something on their own.
 */
export function RecordCard({ title, badge, fields, actions }) {
  return (
    <Card variant="outlined">
      <CardContent>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', gap: 1, mb: 1 }}>
          <Typography sx={{ fontWeight: 600, minWidth: 0, overflowWrap: 'anywhere' }}>{title}</Typography>
          {badge}
        </Stack>
        <Box component="dl" sx={{ display: 'grid', gridTemplateColumns: 'auto 1fr', columnGap: 2, rowGap: 0.5, m: 0 }}>
          {fields.map(([label, value]) => (
            <Box key={label} sx={{ display: 'contents' }}>
              <Typography component="dt" variant="body2" color="text.secondary">{label}</Typography>
              <Typography component="dd" variant="body2" sx={{ m: 0, overflowWrap: 'anywhere' }}>{value}</Typography>
            </Box>
          ))}
        </Box>
        {actions && <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', mt: 1.5 }}>{actions}</Stack>}
      </CardContent>
    </Card>
  );
}
