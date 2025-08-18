module AABBCCDD

go 1.23.4

// uncomment for local development on the superkit core.
replace github.com/khulnasoft/superkit => ../

require (
	github.com/go-chi/chi/v5 v5.2.2
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/khulnasoft/superkit v0.0.0-20250227173556-624132c63837
	github.com/mattn/go-sqlite3 v1.14.22
	golang.org/x/crypto v0.35.0
	gorm.io/driver/sqlite v1.5.6
	gorm.io/gorm v1.25.10
)

require (
	github.com/a-h/templ v0.3.865 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/gorilla/sessions v1.4.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
)
