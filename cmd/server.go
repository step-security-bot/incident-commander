package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/schema/openapi"
	"github.com/flanksource/kopper"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/flanksource/incident-commander/api"
	v1 "github.com/flanksource/incident-commander/api/v1"
	"github.com/flanksource/incident-commander/auth"
	"github.com/flanksource/incident-commander/canary"
	"github.com/flanksource/incident-commander/db"
	"github.com/flanksource/incident-commander/events"
	"github.com/flanksource/incident-commander/jobs"
	"github.com/flanksource/incident-commander/logs"
	"github.com/flanksource/incident-commander/rbac"
	"github.com/flanksource/incident-commander/snapshot"
	"github.com/flanksource/incident-commander/upstream"
	"github.com/flanksource/incident-commander/utils"
)

const (
	HeaderCacheControl = "Cache-Control"
	CacheControlValue  = "public, max-age=2592000, immutable"
)

var cacheSuffixes = []string{
	".ico",
	".svg",
	".css",
	".js",
	".png",
}

func createHTTPServer(gormDB *gorm.DB) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cc := api.NewContext(gormDB, c)
			return next(cc)
		}
	})

	e.Use(echoprometheus.NewMiddleware("mission_control"))
	e.GET("/metrics", echoprometheus.NewHandler())

	kratosHandler := auth.NewKratosHandler(kratosAPI, kratosAdminAPI, db.PostgRESTJWTSecret)
	if enableAuth {
		adminUserID, err := kratosHandler.CreateAdminUser(context.Background())
		if err != nil {
			logger.Fatalf("Failed to created admin user: %v", err)
		}

		middleware, err := kratosHandler.KratosMiddleware()
		if err != nil {
			logger.Fatalf("Failed to initialize kratos middleware: %v", err)
		}
		e.Use(middleware.Session)

		// Initiate RBAC
		if err := rbac.Init(adminUserID); err != nil {
			logger.Fatalf("Failed to initialize rbac: %v", err)
		}
	}

	if postgrestURI != "" {
		forward(e, "/db", postgrestURI, rbac.Authorization(rbac.ObjectDatabase, "any"))
	}

	e.Use(middleware.Logger())
	e.Use(ServerCache)

	e.GET("/health", func(c echo.Context) error {
		if err := db.Pool.Ping(context.Background()); err != nil {
			return c.JSON(http.StatusInternalServerError, api.HTTPError{
				Error:   err.Error(),
				Message: "Failed to ping database",
			})
		}
		return c.JSON(http.StatusOK, api.HTTPSuccess{Message: "ok"})
	})

	e.POST("/auth/invite_user", kratosHandler.InviteUser, rbac.Authorization(rbac.ObjectAuth, rbac.ActionWrite))

	e.GET("/snapshot/topology/:id", snapshot.Topology)
	e.GET("/snapshot/incident/:id", snapshot.Incident)
	e.GET("/snapshot/config/:id", snapshot.Config)

	e.POST("/auth/:id/update_state", auth.UpdateAccountState)
	e.POST("/auth/:id/properties", auth.UpdateAccountProperties)

	e.POST("/rbac/:id/update_role", rbac.UpdateRoleForUser, rbac.Authorization(rbac.ObjectRBAC, rbac.ActionWrite))

	// Serve openapi schemas
	schemaServer, err := utils.HTTPFileserver(openapi.Schemas)
	if err != nil {
		logger.Fatalf("Error creating schema fileserver: %v", err)
	}
	e.GET("/schemas/*", echo.WrapHandler(http.StripPrefix("/schemas/", schemaServer)))

	if api.UpstreamConf.IsPartiallyFilled() {
		logger.Warnf("Please ensure that all the required flags for upstream is supplied.")
	}
	upstreamGroup := e.Group("/upstream")
	upstreamGroup.POST("/push", upstream.PushUpstream)
	upstreamGroup.GET("/pull/:agent_name", upstream.Pull)
	upstreamGroup.GET("/canary/pull/:agent_name", canary.Pull)
	upstreamGroup.GET("/status/:agent_name", upstream.Status)

	forward(e, "/config", configDb)
	forward(e, "/canary", api.CanaryCheckerPath)
	forward(e, "/kratos", kratosAPI)
	forward(e, "/apm", api.ApmHubPath) // Deprecated

	e.POST("/logs", logs.LogsHandler)
	return e
}

func launchKopper() {
	mgr, err := kopper.Manager(&kopper.ManagerOptions{
		AddToSchemeFunc: v1.AddToScheme,
	})
	if err != nil {
		logger.Fatalf("error creating manager: %v", err)
	}

	if err = kopper.SetupReconciler(
		mgr,
		db.PersistConnectionFromCRD,
		db.DeleteConnection,
		"connection.mission-control.flanksource.com",
	); err != nil {
		logger.Fatalf("Unable to create controller for Connection: %v", err)
	}

	if err = kopper.SetupReconciler(
		mgr,
		db.PersistIncidentRuleFromCRD,
		db.DeleteIncidentRule,
		"incidentrule.mission-control.flanksource.com",
	); err != nil {
		logger.Fatalf("Unable to create controller for IncidentRule: %v", err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Fatalf("error running manager: %v", err)
	}
}

var Serve = &cobra.Command{
	Use:    "serve",
	PreRun: PreRun,
	Run: func(cmd *cobra.Command, args []string) {
		// PostgREST needs to know how it is exposed to create the correct links
		db.HttpEndpoint = publicEndpoint + "/db"
		if !enableAuth {
			db.PostgresDBAnonRole = "postgrest_api"
		}

		if postgrestURI != "" {
			parsedURL, err := url.Parse(postgrestURI)
			if err != nil {
				logger.Fatalf("Failed to parse PostgREST URL: %v", err)
			}

			host := strings.ToLower(parsedURL.Hostname())
			if host == "localhost" {
				go db.StartPostgrest(parsedURL.Port())
			}
		}

		go jobs.Start()

		events.StartConsumers(db.Gorm, events.Config{
			UpstreamPush: api.UpstreamConf,
		})

		go launchKopper()

		e := createHTTPServer(db.Gorm)
		listenAddr := fmt.Sprintf(":%d", httpPort)
		logger.Infof("Listening on %s", listenAddr)
		if err := e.Start(listenAddr); err != nil {
			logger.Fatalf("Failed to start server: %v", err)
		}
	},
}

func forward(e *echo.Echo, prefix string, target string, middlewares ...echo.MiddlewareFunc) {
	middlewares = append(middlewares, ModifyKratosRequestHeaders, proxyMiddleware(e, prefix, target))
	e.Group(prefix).Use(middlewares...)
}

func ModifyKratosRequestHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if strings.HasPrefix(c.Request().URL.Path, "/kratos") {
			// Kratos requires the header X-Forwarded-Proto but Nginx sets it as "https,http"
			// This leads to URL malformation further upstream
			val := utils.Coalesce(
				c.Request().Header.Get("X-Forwarded-Scheme"),
				c.Request().Header.Get("X-Scheme"),
				"https",
			)
			c.Request().Header.Set(echo.HeaderXForwardedProto, val)

			// Need to remove the Authorization header set by our auth middleware for kratos
			// since it uses that header to extract token while performing certain actions
			c.Request().Header.Del(echo.HeaderAuthorization)
		}
		return next(c)
	}
}

func proxyMiddleware(e *echo.Echo, prefix, targetURL string) echo.MiddlewareFunc {
	_url, err := url.Parse(targetURL)
	if err != nil {
		e.Logger.Fatal(err)
	}

	return middleware.ProxyWithConfig(middleware.ProxyConfig{
		Rewrite: map[string]string{
			fmt.Sprintf("^%s/*", prefix): "/$1",
		},
		Balancer: middleware.NewRoundRobinBalancer([]*middleware.ProxyTarget{{URL: _url}}),
	})
}

func init() {
	ServerFlags(Serve.Flags())
}

// suffixesInItem checks if any of the suffixes are in the item.
func suffixesInItem(item string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(item, suffix) {
			return true
		}
	}
	return false
}

// ServerCache middleware adds a `Cache Control` header to the response.
func ServerCache(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if suffixesInItem(c.Request().RequestURI, cacheSuffixes) {
			c.Response().Header().Set(HeaderCacheControl, CacheControlValue)
		}
		return next(c)
	}
}
