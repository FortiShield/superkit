package kit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
)

var store *sessions.CookieStore

type HandlerFunc func(kit *Kit) error

type ErrorHandlerFunc func(kit *Kit, err error)

type AuthKey struct{}

type Auth interface {
	Check() bool
}

var (
	errorHandler = func(kit *Kit, err error) {
		kit.Text(http.StatusInternalServerError, err.Error())
	}
)

type DefaultAuth struct{}

func (DefaultAuth) Check() bool { return false }

type Kit struct {
	Response http.ResponseWriter
	Request  *http.Request
}

// UseErrorHandler sets a global error handler used by Handler wrappers.
func UseErrorHandler(h ErrorHandlerFunc) { errorHandler = h }

func (kit *Kit) Auth() Auth {
	value, ok := kit.Request.Context().Value(AuthKey{}).(Auth)
	if !ok {
		slog.Warn("kit authentication not set")
		return DefaultAuth{}
	}
	return value
}

// GetSession returns a session by its name. It will fatal if Setup was not called
// and the session store is not initialized. This enforces explicit initialization
// of the package (Setup) at program start.
func (kit *Kit) GetSession(name string) *sessions.Session {
	if store == nil {
		log.Fatal("session store not initialized: call kit.Setup() before using sessions")
	}
	sess, _ := store.Get(kit.Request, name)
	return sess
}

// Redirect supports HTMX by setting the HX-Redirect response header when the
// request contains an HX-Request header. It uses the provided status for the
// redirect response.
func (kit *Kit) Redirect(status int, url string) error {
	// HTMX clients set the HX-Request header (value may be "true" or non-empty).
	if strings.TrimSpace(kit.Request.Header.Get("HX-Request")) != "" {
		kit.Response.Header().Set("HX-Redirect", url)
		kit.Response.WriteHeader(status)
		return nil
	}
	http.Redirect(kit.Response, kit.Request, url, status)
	return nil
}

func (kit *Kit) FormValue(name string) string {
	// Ensure form is parsed
	_ = kit.Request.ParseForm()
	return kit.Request.PostFormValue(name)
}

// JSON writes a JSON response. Content-Type is set before writing headers.
// It returns any encoding error.
func (kit *Kit) JSON(status int, v any) error {
	kit.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	kit.Response.WriteHeader(status)
	enc := json.NewEncoder(kit.Response)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// JSONError writes a JSON response containing an error message as {"error":"..."}.
func (kit *Kit) JSONError(status int, err error) error {
	if err == nil {
		err = errors.New(http.StatusText(status))
	}
	payload := map[string]string{"error": err.Error()}
	return kit.JSON(status, payload)
}

// Text writes a plain text response. Content-Type is set before writing headers.
func (kit *Kit) Text(status int, msg string) error {
	kit.Response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	kit.Response.WriteHeader(status)
	_, err := kit.Response.Write([]byte(msg))
	return err
}

// Bytes writes raw bytes as a plain text response. Content-Type is set before headers.
func (kit *Kit) Bytes(status int, b []byte) error {
	kit.Response.Header().Set("Content-Type", "application/octet-stream")
	kit.Response.WriteHeader(status)
	_, err := kit.Response.Write(b)
	return err
}

func (kit *Kit) Render(c templ.Component) error {
	return c.Render(kit.Request.Context(), kit.Response)
}

func (kit *Kit) Getenv(name string, def string) string {
	return Getenv(name, def)
}

// BindJSON decodes the request body into v. It disallows unknown fields to help
// catch client mistakes early.
func (kit *Kit) BindJSON(v any) error {
	if kit.Request.Body == nil {
		return errors.New("request body is empty")
	}
	dec := json.NewDecoder(kit.Request.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Query returns the given query parameter or a default if missing.
func (kit *Kit) Query(name, def string) string {
	val := kit.Request.URL.Query().Get(name)
	if val == "" {
		return def
	}
	return val
}

func Handler(h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kit := &Kit{
			Response: w,
			Request:  r,
		}
		if err := h(kit); err != nil {
			if errorHandler != nil {
				errorHandler(kit, err)
				return
			}
			// fallback
			_ = kit.Text(http.StatusInternalServerError, err.Error())
		}
	}
}

type AuthenticationConfig struct {
	AuthFunc    func(*Kit) (Auth, error)
	RedirectURL string
}

// WithAuthentication wraps an http.Handler to run an authentication function and
// optionally enforce authentication strictly. On success the Auth value is added
// to the request context.
func WithAuthentication(config AuthenticationConfig, strict bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kit := &Kit{
				Response: w,
				Request:  r,
			}
			auth, err := config.AuthFunc(kit)
			if err != nil {
				if errorHandler != nil {
					errorHandler(kit, err)
					return
				}
				kit.Text(http.StatusInternalServerError, err.Error())
				return
			}
			if strict && !auth.Check() && r.URL.Path != config.RedirectURL {
				_ = kit.Redirect(http.StatusSeeOther, config.RedirectURL)
				return
			}
			ctx := context.WithValue(r.Context(), AuthKey{}, auth)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func Getenv(name string, def string) string {
	env := os.Getenv(name)
	if len(env) == 0 {
		return def
	}
	return env
}

func IsDevelopment() bool {
	return os.Getenv("SUPERKIT_ENV") == "development"
}

func IsProduction() bool {
	return os.Getenv("SUPERKIT_ENV") == "production"
}

func Env() string {
	return os.Getenv("SUPERKIT_ENV")
}

// Setup initializes environment and session store. It no longer fatals when
// .env is missing (which is common in containerized deployments), but it will
// exit if a sufficiently strong SUPERKIT_SECRET is not provided.
func Setup() {
	// Load .env if present; continue if not found.
	if err := godotenv.Load(); err != nil {
		// Only log; do not exit. Many deployments provide env via environment variables.
		slog.Warn("no .env file loaded; continuing with environment variables")
	}

	appSecret := os.Getenv("SUPERKIT_SECRET")
	if len(appSecret) < 32 {
		// For security reasons exit if SUPERKIT_SECRET is not sufficiently strong.
		fmt.Println("invalid or missing SUPERKIT_SECRET variable. Set SUPERKIT_SECRET in your environment to at least 32 characters.")
		os.Exit(1)
	}

	store = sessions.NewCookieStore([]byte(appSecret))

	// Configure session options from environment with sensible defaults.
	maxAge := 60 * 60 * 24 * 30 // 30 days
	if v := os.Getenv("SUPERKIT_SESSION_MAXAGE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			maxAge = i
		} else {
			slog.Warn("invalid SUPERKIT_SESSION_MAXAGE, using default", "value", v)
		}
	}

	secure := IsProduction()
	if v := strings.ToLower(os.Getenv("SUPERKIT_SESSION_SECURE")); v != "" {
		if v == "true" || v == "1" || v == "yes" {
			secure = true
		} else if v == "false" || v == "0" || v == "no" {
			secure = false
		}
	}

	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}

	// Optional: log startup time for diagnostics.
	slog.Info("kit setup complete", "env", Env(), "session_maxage", store.Options.MaxAge, "secure_cookie", store.Options.Secure, "timestamp", time.Now().UTC().Format(time.RFC3339))
}
