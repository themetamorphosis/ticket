import { createTheme } from '@mui/material/styles';

// Material, tuned to a Material 3 tone: a larger shape scale, tonal fills
// instead of grey hairlines, and pill controls. No colour is hard-coded here:
// every fill is theme.alpha() over a palette key, so it follows the palette
// in both colour schemes. See docs/DESIGN_GUIDELINES.md §2.

export const CHIP_COLORS = ['primary', 'secondary', 'success', 'warning', 'error', 'info'];

// cssVariables is on, so palette values are CSS variables in both schemes.
const palette = (theme) => theme.vars.palette;

/** A translucent fill over a palette colour (`key` is e.g. 'primary'). */
export const tonal = (theme, key, opacity) => theme.alpha(palette(theme)[key].main, opacity);

/** Text colour for a tonal fill: the dark shade on light, the light shade on dark. */
const onTonal = (theme, key) => ({
  color: palette(theme)[key].dark,
  ...theme.applyStyles('dark', { color: palette(theme)[key].light }),
});

export const theme = createTheme({
  // CSS variables switched by prefers-color-scheme: a static export has no
  // JavaScript before first paint, so the scheme cannot be picked in script.
  cssVariables: { colorSchemeSelector: 'media' },
  colorSchemes: { light: true, dark: true },
  shape: { borderRadius: 12 },
  typography: {
    fontFamily: "'Onest Variable', system-ui, -apple-system, 'Segoe UI', 'Noto Sans Bengali', sans-serif",
    button: { textTransform: 'none', fontWeight: 600 },
  },
  components: {
    MuiButton: {
      defaultProps: { disableElevation: true },
      styleOverrides: { root: { borderRadius: 999 } },
    },
    MuiCard: { styleOverrides: { root: { borderRadius: 16 } } },
    MuiPaper: { styleOverrides: { outlined: { borderRadius: 16 } } },
    MuiDialog: {
      styleOverrides: {
        // Below sm a dialog takes back its 32px margin, so a phone dialog is
        // not a sliver.
        paper: ({ theme: t }) => ({
          borderRadius: 28,
          [t.breakpoints.down('sm')]: { margin: t.spacing(2), width: `calc(100% - ${t.spacing(4)})` },
        }),
      },
    },
    MuiChip: {
      styleOverrides: { root: { fontWeight: 600 } },
      variants: [
        {
          props: { variant: 'filled', color: 'default' },
          style: ({ theme: t }) => ({ backgroundColor: tonal(t, 'primary', 0.12), ...onTonal(t, 'primary') }),
        },
        ...CHIP_COLORS.map((key) => ({
          props: { variant: 'filled', color: key },
          style: ({ theme: t }) => ({ backgroundColor: tonal(t, key, 0.16), ...onTonal(t, key) }),
        })),
      ],
    },
    MuiTabs: { defaultProps: { variant: 'scrollable', allowScrollButtonsMobile: true } },
    MuiTableCell: {
      styleOverrides: {
        // A tinted header row instead of a heavy rule; it scrolls with the table.
        head: ({ theme: t }) => ({ backgroundColor: tonal(t, 'primary', 0.06), fontWeight: 600, whiteSpace: 'nowrap' }),
      },
    },
    MuiAppBar: { defaultProps: { elevation: 0, color: 'inherit' } },
    MuiListItemButton: {
      styleOverrides: {
        root: ({ theme: t }) => ({
          borderRadius: 999,
          '&.Mui-selected': { backgroundColor: tonal(t, 'primary', 0.12), ...onTonal(t, 'primary') },
        }),
      },
    },
  },
});
