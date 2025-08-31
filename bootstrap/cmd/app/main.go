package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"app"
	"public"

	"github.com/go-chi/chi/v5"
	"github.com/khulnasoft/superkit/kit"
)

func main() {
	// Initialize kit (loads env, validates secret, configures session store).
	kit.Setup()

	// Create router and let app initialize middleware and routes.
	router := chi.NewMux()
	app.InitializeMiddleware(router)

	// Serve static assets. In development we disable caching to make iteration easier.
	if kit.IsDevelopment() {
		router.Handle("/public/*", disableCache(staticDev()))
	} else if kit.IsProduction() {
		router.Handle("/public/*", staticProd())
	}

	// Use the application's error handler globally.
	kit.UseErrorHandler(app.ErrorHandler)

	// Register not-found handler and app routes, and application events.
	router.HandleFunc("/*", kit.Handler(app.NotFoundHandler))
	app.InitializeRoutes(router)
	app.RegisterEvents()

	// Listen address with sensible default.
	listenAddr := os.Getenv("HTTP_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	// Human-friendly URL for logs (in development, Templ proxy is expected).
	url := "http://localhost:7331"
	if kit.IsProduction() {
		url = fmt.Sprintf("http://localhost%s", listenAddr)
	}

	log.Printf("application running in %s at %s\n", kit.Env(), url)

	// Create server to allow graceful shutdown.
	srv := &http.Server{
		Addr:    listenAddr,
		Handler: router,
	}

	// Start server in a goroutine.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with a timeout.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Printf("shutdown signal received, shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Printf("server stopped")
}

func staticDev() http.Handler {
	return http.StripPrefix("/public/", http.FileServer(http.FS(os.DirFS("public"))))
}

func staticProd() http.Handler {
	return http.StripPrefix("/public/", http.FileServer(public.AssetsFS))
}

func disableCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
