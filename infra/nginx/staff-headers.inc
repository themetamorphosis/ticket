# Security headers for the staff site. Included in every location that sets
# its own headers, because nginx drops inherited add_header directives there.
add_header X-Content-Type-Options nosniff always;
add_header Referrer-Policy no-referrer always;
add_header X-Frame-Options DENY always;
add_header Permissions-Policy "camera=(self), microphone=(), geolocation=()" always;
