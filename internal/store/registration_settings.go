package store

import (
	"context"
	"github.com/jackc/pgx/v5"
)

type RegistrationFlags struct {
	Enabled       bool `json:"enabled"`
	CodeRequired  bool `json:"code_required"`
	EmailRequired bool `json:"email_required"`
}
type registrationQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func registrationFlags(ctx context.Context, q registrationQuery) (RegistrationFlags, error) {
	var mode string
	var enabled, code, email *bool
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM settings WHERE key='registration_mode'),'manual'),(SELECT value::boolean FROM settings WHERE key='registration_enabled'),(SELECT value::boolean FROM settings WHERE key='registration_code_required'),(SELECT value::boolean FROM settings WHERE key='registration_email_required')`).Scan(&mode, &enabled, &code, &email)
	flags := RegistrationFlags{Enabled: mode == "code" || mode == "email" || mode == "both" || mode == "open", CodeRequired: mode == "code" || mode == "both", EmailRequired: mode == "email" || mode == "both"}
	if enabled != nil {
		flags.Enabled = *enabled
	}
	if code != nil {
		flags.CodeRequired = *code
	}
	if email != nil {
		flags.EmailRequired = *email
	}
	return flags, err
}
func (s *Store) Registration(ctx context.Context) (RegistrationFlags, error) {
	return registrationFlags(ctx, s.Pool)
}
