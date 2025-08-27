package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
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
	flag.Parse()

	var err error

	app := Contacter{
		echo:        echo.New(),
		configCache: NewConfigCache(*configDir),
		staticDir:   *staticDir,
	}

	app.echo.Renderer, err = NewTemplateRegistry(*templatesDir)
	if err != nil {
		app.echo.Logger.Fatalf("failed to initialize template registry: %v", err)
	}

	app.echo.IPExtractor = app.IPExtractor

	// Rate limiter middleware for POST requests (1 per minute per IP)
	rateLimiter := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Every(5 * time.Minute), // 1 token every 5 minutes
				Burst:     1,                           // max 1 request at a time
				ExpiresIn: 30 * time.Minute,            // how long visitor entry stays around
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(context echo.Context, err error) error {
			return context.Render(http.StatusTooManyRequests, context.Request().Host, TemplateData{Error: "Too many requests, please try again later"})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.Render(http.StatusTooManyRequests, context.Request().Host, TemplateData{Error: "Too many requests, please try again later"})
		},
	})

	// Route for contact form
	app.echo.GET("/contact", app.handleContact)
	app.echo.POST("/contact", app.handleContact, rateLimiter)

	// Route for static files (css, js, images)
	app.echo.GET("/contact/*", app.handleStatic)

	// Start server
	app.echo.Logger.Fatal(app.echo.Start(":8080"))
}

func (c *Contacter) IPExtractor(req *http.Request) string {
	cfg, err := c.configCache.Get(req.Host)
	if err != nil {
		c.echo.Logger.Errorf("IPExtractor Configuration Failed, using DirectIP: %v", err)
		return echo.ExtractIPDirect()(req)
	}

	switch strings.ToLower(cfg.Upstream.Method) {
	case "direct":
		return echo.ExtractIPDirect()(req)
	case "x-forwarded-for":
		return echo.ExtractIPFromXFFHeader(networksToTrustOptions(cfg.Upstream.Whitelist...)...)(req)
	case "x-real-ip":
		return echo.ExtractIPFromXFFHeader(networksToTrustOptions(cfg.Upstream.Whitelist...)...)(req)
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
