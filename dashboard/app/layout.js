import './globals.css';

export const metadata = { title: 'Rehearse', description: 'Incidents, approvals and audit for the Rehearse Safety Gate' };

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>
        <header>
          <a href="/" className="brand">Rehearse</a>
          <nav><a href="/">Incidents</a><a href="/audit">Audit history</a></nav>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
