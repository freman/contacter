# Contacter

Contacter is a lightweight, multi-domain contact form service.
It was originally built because I needed a reliable way for Google to index a contact form — and it grew into a more general-purpose service.

## Features

- Multi-domain support
  Serve different templates per domain, or fall back to a common template for all.

- Built-in rate limiting
  Prevents abuse by limiting request frequency per client.

- AbuseIPDB integration
  Checks inbound IP addresses against https://www.abuseipdb.com/ to detect and block known bad actors.

- SMTP sender verification
  Optionally probes the sender’s mail server to verify that the account exists before sending.

- Simple configuration
  Flexible per-domain configuration with JSON files.

---

## Configuration

Contacter accepts the following flags:

- configDir — directory containing per-domain JSON config files
- templatesDir — directory of HTML templates for rendering forms and responses
- staticDir — directory of static assets (CSS, JS, images, etc.)

---

## Example Configuration

Save as config/example.com.conf:

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
      }
    }

### upstream.method
Controls how Contacter extracts the client IP:

- direct — use the TCP connection source address
- x-forwarded-for — use the X-Forwarded-For header
- x-real-ip — use the X-Real-IP header

---

## Usage

1. Create a configuration file per domain under config/
2. Place your HTML templates under templates/
3. (Optional) Add static assets under static/
4. Run Contacter:

    go run ./cmd/contacter \
      -config ./config \
      -templates ./templates \
      -static ./static

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

In short: Contacter always tries to find a domain-specific config, template, or static directory first — and falls back to `default` if none is found.

## Roadmap / Ideas

- Support for external rate limiter backends (Redis, Memcached)
- Support for per domain rate limiting
- Webhook integration for alternative notification channels (Slack, Discord, etc.)
- Extended template variables (geoIP, reputation score, etc.)

---

## License

MIT — use freely, modify, and contribute back!
