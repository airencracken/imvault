package store

import (
	"context"
	"github.com/airencracken/comfylib/displaytime"
)

func (s *Store) Timezone(ctx context.Context, id int64) (string, error) {
	return displaytime.Read(ctx, s.db, id)
}
func (s *Store) SetTimezone(ctx context.Context, id int64, name string) error {
	return displaytime.Save(ctx, s.db, `INSERT INTO comfy_display_timezones(member,timezone) SELECT id,? FROM users WHERE id=? AND disabled=0 ON CONFLICT(member) DO UPDATE SET timezone=excluded.timezone`, id, name)
}
