package middleware

import (
	"context"
	"net/http"
)

type contextKey string

const (
	requestKey         contextKey = "middleware.request"
	responseHeadersKey contextKey = "middleware.responseHeaders"
)

// WithRequest attaches the *http.Request to the request context so downstream
// handlers/middlewares can access it via RequestFromContext.
func WithRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithResponseHeaders creates an http.Header map and attaches it to the context.
// Downstream handlers can add headers via ResponseHeadersFromContext and those
// headers can be applied to the ResponseWriter afterwards.
func WithResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Initialize a mutable header map in the context.
		ctx := context.WithValue(r.Context(), responseHeadersKey, http.Header{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithRequestAndResponseHeaders combines WithRequest and WithResponseHeaders.
func WithRequestAndResponseHeaders(next http.Handler) http.Handler {
	return WithRequest(WithResponseHeaders(next))
}

// RequestFromContext returns the *http.Request that was stored by WithRequest.
// The boolean indicates whether a request was found.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	v := ctx.Value(requestKey)
	if v == nil {
		return nil, false
	}
	req, ok := v.(*http.Request)
	return req, ok
}

// ResponseHeadersFromContext returns the http.Header map stored by WithResponseHeaders.
// The returned header is mutable and changes will be visible to any code that has
// the same header reference from the context.
func ResponseHeadersFromContext(ctx context.Context) (http.Header, bool) {
	v := ctx.Value(responseHeadersKey)
	if v == nil {
		return nil, false
	}
	h, ok := v.(http.Header)
	return h, ok
}

// SetResponseHeader is a convenience helper that sets a header value on the
// context-stored header map (created by WithResponseHeaders). If no header map
// exists, this is a no-op.
func SetResponseHeader(ctx context.Context, key, value string) {
	if h, ok := ResponseHeadersFromContext(ctx); ok {
		h.Set(key, value)
	}
}

// ApplyResponseHeaders applies any headers found in the context to the provided
// http.ResponseWriter. This is intended to be called after handler execution
// (for example in a "final" middleware) so accumulated headers are written out.
func ApplyResponseHeaders(w http.ResponseWriter, ctx context.Context) {
	if h, ok := ResponseHeadersFromContext(ctx); ok {
		for k, vals := range h {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
	}
}
