import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';

/** One headline metric. */
export function StatCard({ icon, label, value }) {
  return (
    <Card variant="outlined">
      <CardContent>
        <Stack direction="row" sx={{ alignItems: 'center', gap: 1, color: 'text.secondary' }}>
          {icon}
          <Typography variant="body2">{label}</Typography>
        </Stack>
        <Typography variant="h5" component="p" sx={{ fontWeight: 700, mt: 1 }}>
          {value}
        </Typography>
      </CardContent>
    </Card>
  );
}

/** A responsive grid of StatCards. */
export function StatGrid({ children }) {
  return (
    <Stack
      sx={{
        display: 'grid',
        gap: 2,
        gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', sm: 'repeat(auto-fill, minmax(12rem, 1fr))' },
        mb: 4,
      }}
    >
      {children}
    </Stack>
  );
}
