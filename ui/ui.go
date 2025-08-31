package ui

import (
	"strings"

	"github.com/a-h/templ"
)

// CreateAttrs creates a templ.Attributes map and ensures the "class" attribute
// is initialized to the combination of baseClass and defaultClass. Optional
// option callbacks may modify the attributes further.
func CreateAttrs(baseClass string, defaultClass string, opts ...func(*templ.Attributes)) templ.Attributes {
	attrs := templ.Attributes{}
	attrs["class"] = joinClasses(baseClass, defaultClass)
	for _, o := range opts {
		if o != nil {
			o(&attrs)
		}
	}
	return attrs
}

// Merge joins two class strings into a normalized class list (trimmed, deduplicated,
// and space-separated).
func Merge(a, b string) string {
	return joinClasses(a, b)
}

// Class returns an option function that appends the given class (or classes)
// to the "class" attribute on templ.Attributes in a safe, normalized way.
func Class(c string) func(*templ.Attributes) {
	return func(attrs *templ.Attributes) {
		if attrs == nil {
			return
		}
		current, _ := (*attrs)["class"].(string)
		(*attrs)["class"] = joinClasses(current, c)
	}
}

// Attr returns an option function that sets a specific attribute key to value.
// Useful for setting attributes other than "class".
func Attr(key string, value interface{}) func(*templ.Attributes) {
	return func(attrs *templ.Attributes) {
		if attrs == nil {
			return
		}
		(*attrs)[key] = value
	}
}

// joinClasses accepts any number of class strings, splits them on whitespace,
// trims them, removes duplicates while preserving first-seen order, and returns
// a single space-separated class string.
func joinClasses(classes ...string) string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, cs := range classes {
		if cs == "" {
			continue
		}
		parts := strings.Fields(cs)
		for _, p := range parts {
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
