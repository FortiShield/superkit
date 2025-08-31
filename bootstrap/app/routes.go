package app

import (
	"AABBCCDD/app/handlers"
	"AABBCCDD/app/views/errors"
	"AABBCCDD/plugins/auth"
	"net/http"
	"time"

	"log/slog"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/khulnasoft/superkit/kit"
	"github.com/khulnasoft/superkit/kit/middleware"
)

// InitializeMiddleware wires up global middleware for the application router.
// Enhancements:
// - Added RequestID and RealIP middleware for better observability.
// - Replaced the single WithRequest middleware with WithRequestAndResponseHeaders
//   so handlers can accumulate response headers in context.
// - Added a final middleware that applies accumulated response headers and logs timing.
func InitializeMiddleware(router *chi.Mux) {
	// Standard Chi middleware
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.RealIP)
	router.Use(chimiddleware.Logger)
	router.Use(chimiddleware.Recoverer)

	// App-level middleware from kit
	router.Use(middleware.WithRequestAndResponseHeaders)

	// Final middleware: apply any headers accumulated in context and log request timing.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// Serve the request
			next.ServeHTTP(w, r)
			// Apply headers that handlers might have added to the context
			middleware.ApplyResponseHeaders(w, r.Context())

			// Light-weight structured debug info
			slog.Debug("request complete",
				"method", r.Method,
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
				"duration", time.Since(start))
		})
	})
}

// InitializeRoutes registers all application routes and authentication wiring.
//
// Improvements:
// - Centralized authConfig creation.
// - Added a simple /healthz endpoint.
// - Used chi's Route/Group helpers with kit.WithAuthentication so intent is explicit.
func InitializeRoutes(router *chi.Mux) {
	// Initialize auth plugin routes (login, logout, etc).
	auth.InitializeRoutes(router)

	authConfig := kit.AuthenticationConfig{
		AuthFunc:    auth.AuthenticateUser,
		RedirectURL: "/login",
	}

	// Public / optionally-authenticated routes (auth present if available)
	router.Group(func(r chi.Router) {
		r.Use(kit.WithAuthentication(authConfig, false)) // non-strict: auth may be present
		r.Get("/", kit.Handler(handlers.HandleLandingIndex))
		// health check
		r.Get("/healthz", kit.Handler(func(k *kit.Kit) error {
			// lightweight health endpoint used by load balancers and runtime checks
			return k.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}))
	})

	// Strictly authenticated routes
	router.Group(func(r chi.Router) {
		r.Use(kit.WithAuthentication(authConfig, true)) // strict: redirect to login when unauthenticated
		// register authenticated routes here, e.g.:
		// r.Get("/dashboard", kit.Handler(handlers.HandleDashboard))
	})
}

// NotFoundHandler is used when no route matches the request.
func NotFoundHandler(k *kit.Kit) error {
	// Ensure correct status code is returned to clients before rendering the view.
	k.Response.WriteHeader(http.StatusNotFound)
	return k.Render(errors.Error404())
}

// ErrorHandler is the centralized error handler used by kit.Handler wrapper.
func ErrorHandler(k *kit.Kit, err error) {
	// Log with context for easier debugging in observability systems.
	slog.Error("internal server error",
		"err", err.Error(),
		"method", k.Request.Method,
		"path", k.Request.URL.Path,
		"remote", k.Request.RemoteAddr,
	)

	// Render a friendly error page to the user.
	k.Response.WriteHeader(http.StatusInternalServerError)
	_ = k.Render(errors.Error500())
}
