# Contacter

Contacter is a lightweight, multi-domain contact form service.
It was originally built because I needed a reliable way for Google to index a contact form - and it grew into a more general-purpose service.

## Features

- Multi-domain support
  Serve different templates per domain, or fall back to a common template for all.

- Spam protection
  Honeypot fields, JavaScript setvalue checks, and HMAC-signed single-use tokens - configure any combination.

- IP reputation cache
  Tracks seen IPs with score and source. Spam-triggered blocks are recorded locally so bots can't retry. Optionally backed by AbuseIPDB for external reputation data.

- Per-domain and global rate limiting
  Per-client rate limiting, configurable per domain or globally via `global.conf`.

- SMTP with TLS support
  Plain SMTP, STARTTLS, and implicit TLS. Authenticated SMTP for relay setups. Email addresses are validated before connecting.

- SMTP sender verification
  Optionally probes the sender's mail server to verify the account exists before sending.

- XHR / fetch support
  Can respond with JSON instead of HTML, so you can embed a contact form on a separate site and POST to Contacter via JavaScript.

- Simple configuration
  Flexible per-domain configuration with JSON files.

---

## Configuration

Contacter accepts the following flags:

- `-config` - directory containing per-domain JSON config files
- `-templates` - directory of HTML templates for rendering forms and responses
- `-static` - directory of static assets (CSS, JS, images, etc.)
- `-listen` - address to listen on (default: `127.0.0.1:8080`)

---

## Example Configuration

Save as `config/example.com.conf`:

    {
      "upstream": {
        "whitelist": [
          "10.0.0.0/8"
        ],
        "method": "x-forwarded-for"
      },
      "smtp": {
        "host": "mail.example.com",
        "port": 587,
        "recipient": "siteowner@example.com",
        "starttls": true
      },
      "smtptest": {
        "enabled": true,
        "helo": "example.com",
        "from": "smtp-user-check@example.com"
      },
      "cache": {
        "file": "cache.json"
      },
      "abuseipdb": {
        "enabled": true,
        "api_key": "your key goes here"
      },
      "cors": {
        "origins": ["https://example.com"]
      },
      "spam_protection": [
        { "field": "yourwebsite",  "type": "honeypot" },
        { "field": "javascript",   "type": "setvalue", "value": "42" },
        { "field": "auth",         "type": "token",    "value": "replace-with-a-long-random-secret" }
      ]
    }

---

## Spam Protection

Three mechanisms are available under `spam_protection`. Configure any combination; unused types are not checked.

### Honeypot

A field name that should always be empty. Real users never see it (hidden via CSS in your template). Bots that populate every field trip it.

    { "field": "yourwebsite", "type": "honeypot" }

### Setvalue

A hidden field whose value is set by a JavaScript snippet at page load. The server checks for the expected value; anything that skipped JavaScript fails.

    { "field": "javascript", "type": "setvalue", "value": "42" }

### Token

On page load, the form fetches a short-lived HMAC-signed token from the API endpoint via a GET request. The token is valid for ten minutes and is single-use - replays are rejected. Expired tokens return an error with the form data available for prefilling.

    { "field": "auth", "type": "token", "value": "replace-with-a-long-random-secret" }

The GET endpoint returns:

    { "token": "<signed-value>" }

Expired or replayed tokens return:

    { "error": "Your session expired - please try again" }

---

## IP Reputation Cache

Contacter maintains a local JSON cache of IP reputation scores. It is consulted at the start of every request; IPs with a score above 50 recorded within the last 24 hours are blocked immediately.

    {
      "cache": {
        "file": "cache.json"
      }
    }

Any IP that trips a spam filter is written into the cache with score 100 and the source set to the filter type that caught it (`honeypot`, `setvalue`, or `token`). This blocks that IP on every subsequent request for 24 hours without needing to re-check the spam filters or call any external API.

The cache file is human-readable JSON:

    {
      "entries": {
        "1.2.3.4": {
          "score": 100,
          "source": "honeypot",
          "timestamp": "2026-05-17T10:00:00Z"
        }
      }
    }

The cache is independent of AbuseIPDB. Configure it without AbuseIPDB to get local-only blocking, or with AbuseIPDB to combine local and external reputation data.

---

## AbuseIPDB

    {
      "abuseipdb": {
        "enabled": true,
        "api_key": "your key goes here"
      }
    }

When enabled, Contacter checks the sender's IP against https://www.abuseipdb.com/ before processing. High-confidence scores (above 50) block the request. Results are written into the shared IP reputation cache, so repeat submissions from the same IP don't generate additional API calls. If the API is unreachable, the check fails open.

Requires `cache.file` to be set.

---

## SMTP

    {
      "smtp": {
        "host": "mail.example.com",
        "port": 587,
        "recipient": "you@example.com",
        "username": "smtp-user",
        "password": "smtp-password",
        "starttls": true
      }
    }

- `starttls: true` - upgrades the connection via STARTTLS (typically port 587)
- `tls: true` - implicit TLS from the start (typically port 465)
- Omit both for plain SMTP
- `username` and `password` enable SMTP AUTH for relay setups

Email addresses are validated against RFC 5322 before a connection is opened.

---

## upstream.method

Controls how Contacter extracts the client IP:

- `direct` - use the TCP connection source address
- `x-forwarded-for` - use the X-Forwarded-For header
- `x-real-ip` - use the X-Real-IP header

---

## cors.origins

A list of origins allowed to POST to this domain's endpoint via XHR or fetch.
If empty or omitted, cross-origin requests are rejected by the browser.

Only list origins you actually control. Do not use `*`.

---

## rate_limit

Controls how aggressively Contacter limits POST requests from a single IP to this domain.
All fields are optional - omitting the block or leaving values at zero uses the defaults.

- `rate` - requests per second as a decimal (default: `0.00333` - one per 5 minutes)
- `burst` - how many requests can land in quick succession before limiting kicks in (default: `1`)
- `expires_in` - seconds before an idle IP's counter is evicted from memory (default: `1800`)

### Global rate limiting

To apply a rate limit across all domains, create `global.conf` in the config directory:

    {
      "rate_limit": {
        "rate": 0.00333,
        "burst": 2,
        "expires_in": 1800
      }
    }

The global limiter runs in addition to any per-domain limit - a request has to pass both.
If `global.conf` is absent or its `rate_limit` block is all zeros, no global limit is applied.
The file is watched for changes and reloaded automatically.

---

## Usage

1. Create a configuration file per domain under `config/`
2. Place your HTML templates under `templates/`
3. (Optional) Add static assets under `static/`
4. Run Contacter:

       go run . \
         -config ./config \
         -templates ./templates \
         -static ./static

---

## Embedding via JavaScript

If you want to host the form on your own site and POST to Contacter rather than linking out to it, set `cors.origins` in the config and use `fetch` with `Accept: application/json`:

    const form = document.getElementById('contact-form');

    // Fetch a token on page load if using token spam protection
    const tokenRes = await fetch('https://contact.example.com/contact', {
      headers: { 'Accept': 'application/json' },
    });
    const { token } = await tokenRes.json();
    document.querySelector('[name="auth"]').value = token;

    form.addEventListener('submit', async (e) => {
      e.preventDefault();

      const res = await fetch('https://contact.example.com/contact', {
        method: 'POST',
        headers: { 'Accept': 'application/json' },
        body: new FormData(form),
      });

      const data = await res.json();

      if (data.error) {
        // show data.error to the user
      } else {
        // show success message
      }
    });

When Contacter sees `Accept: application/json` (or `X-Requested-With: XMLHttpRequest`) it returns:

    { "success": true }

or

    { "error": "some message" }

The direct-form path (`GET /contact`) still works as before for no-JS fallback.

---

## Multiple Domains

Contacter can serve different content depending on the domain being requested.

The lookup order works like this:

1. **Config file**
   - If a config file matching the domain exists (e.g. `example.com.conf`), it will be used.
   - If not, Contacter falls back to `default.conf`.

2. **Templates**
   - If a domain-specific directory exists (e.g. `templates/example.com/`), templates will be loaded from there.
   - If not, templates fall back to `templates/default/`.

3. **Static files**
   - If a domain-specific directory exists (e.g. `static/example.com/`), static files will be served from there.
   - If not, static files fall back to `static/default/`.

In short: Contacter always tries to find a domain-specific config, template, or static directory first - and falls back to `default` if none is found.

---

## License

MIT - use freely, modify, and contribute back!
