import './globals.css';

export const metadata = {
  title: 'Kacheem Comms · Staff',
  description: 'Organizer dashboard and gate scanner.',
  robots: { index: false, follow: false },
};

export const viewport = {
  width: 'device-width',
  initialScale: 1,
  viewportFit: 'cover',
};

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
