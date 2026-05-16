# Contacter

Contacter is a lightweight, multi-domain contact form service.
It was originally built because I needed a reliable way for Google to index a contact form - and it grew into a more general-purpose service.

## Features

- Multi-domain support
  Serve different templates per domain, or fall back to a common template for all.

- Per-domain and global rate limiting
  Per-client rate limiting, configurable per domain or globally via `global.conf`.

- AbuseIPDB integration
  Checks inbound IP addresses against https://www.abuseipdb.com/ to detect and block known bad actors.

- SMTP sender verification
  Optionally probes the sender's mail server to verify that the account exists before sending.

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
        "host": "20.0.0.2",
        "port": 25,
        "recipient": "siteowner@example.com"
      },
      "smtptest": {
        "enabled": true,
        "helo": "example.com",
        "from": "smtp-user-check@example.com"
      },
      "abuseipdb": {
        "enabled": true,
        "api_key": "your key goes here",
        "cache_file": "cache.json"
      },
      "cors": {
        "origins": ["https://example.com"]
      }
    }

### upstream.method
Controls how Contacter extracts the client IP:

- `direct` - use the TCP connection source address
- `x-forwarded-for` - use the X-Forwarded-For header
- `x-real-ip` - use the X-Real-IP header

### cors.origins
A list of origins allowed to POST to this domain's endpoint via XHR or fetch.
If empty or omitted, cross-origin requests are rejected by the browser.

Only list origins you actually control. Do not use `*`.

### rate_limit
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

## Roadmap / Ideas

- Support for external rate limiter backends (Redis, Memcached)
- Webhook integration for alternative notification channels (Slack, Discord, etc.)
- Extended template variables (geoIP, reputation score, etc.)

---

## License

MIT - use freely, modify, and contribute back!
