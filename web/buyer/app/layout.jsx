import './globals.css';

export const metadata = {
  title: 'Kacheem Comms',
  description: 'Concert tickets from Kacheem Comms.',
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
