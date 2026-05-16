package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

type Contacter struct {
	echo        *echo.Echo
	configCache *ConfigCache
	staticDir   string
}

func main() {
	configDir := flag.String("config", "config", "directory containing configuration files")
	templatesDir := flag.String("templates", "templates", "directory containing templates")
	staticDir := flag.String("static", "static", "directory containing static files")
	listen := flag.String("listen", "127.0.0.1:8080", "listen on this address")
	flag.Parse()

	var err error

	app := Contacter{
		echo:        echo.New(),
		configCache: NewConfigCache(*configDir),
		staticDir:   *staticDir,
	}

	app.echo.Renderer, err = NewTemplateRegistry(*templatesDir)
	if err != nil {
		slog.Error("failed to initialize template registry", "err", err)
		os.Exit(1)
	}

	app.echo.IPExtractor = app.IPExtractor

	app.echo.Use(middleware.BodyLimit(200 * 1024))
	app.echo.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		UnsafeAllowOriginFunc: func(c *echo.Context, origin string) (string, bool, error) {
			cfg, err := app.configCache.Get(c.Request().Host)
			if err != nil {
				return "", false, nil
			}

			if slices.Contains(cfg.CORS.Origins, "*") {
				slog.Warn("wildcard CORS origin rejected - list specific origins", "host", c.Request().Host)

				return "", false, nil
			}

			if !slices.Contains(cfg.CORS.Origins, origin) {
				return "", false, nil
			}

			return origin, true, nil
		},
		AllowMethods: []string{"GET", "POST", "OPTIONS"},
		AllowHeaders: []string{"Content-Type", "X-Requested-With"},
		MaxAge:       86400,
	}))

	domainRL := NewDomainRateLimiter(app.configCache)

	app.echo.GET("/contact", app.handleContact)
	app.echo.POST("/contact", app.handleContact, domainRL.Middleware())
	app.echo.GET("/contact/*", app.handleStatic)

	if err := app.echo.Start(*listen); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func (c *Contacter) IPExtractor(req *http.Request) string {
	cfg, err := c.configCache.Get(req.Host)
	if err != nil {
		slog.Error("IPExtractor configuration failed, using direct IP", "err", err)

		return echo.ExtractIPDirect()(req)
	}

	switch strings.ToLower(cfg.Upstream.Method) {
	case "direct":
		return echo.ExtractIPDirect()(req)
	case "x-forwarded-for":
		return echo.ExtractIPFromXFFHeader(networksToTrustOptions(cfg.Upstream.Whitelist...)...)(req)
	case "x-real-ip":
		return echo.ExtractIPFromRealIPHeader(networksToTrustOptions(cfg.Upstream.Whitelist...)...)(req)
	}

	return echo.ExtractIPDirect()(req)
}

func networksToTrustOptions(networks ...Network) []echo.TrustOption {
	result := make([]echo.TrustOption, len(networks))

	for i, network := range networks {
		result[i] = echo.TrustIPRange(network.IPNet)
	}

	return result
}
