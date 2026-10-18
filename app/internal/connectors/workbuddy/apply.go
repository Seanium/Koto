package workbuddy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

func (c *connector) Apply(ctx context.Context, plan *engine.DeliveryPlan) (core.JsonObject, error) {
	if plan.Harness != c.harness || plan.Sink != c.id || plan.Target.Profile != c.id || plan.Target.Root != c.root || plan.Mode != "create" || !uuid.MatchString(plan.Target.Session) || plan.Target.Path != c.transcript(plan.Target.Cwd, plan.Target.Session) {
		return nil, fmt.Errorf("WorkBuddy plan target mismatch")
	}
	db, err := c.open(true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	owner, err := c.owner(ctx, db, plan.Target.Account)
	if err != nil {
		return nil, err
	}
	if owner != core.S(plan.Extra["owner"]) {
		return nil, fmt.Errorf("WorkBuddy target account changed after preview")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var oldOwner, oldCwd, transport string
	err = tx.QueryRowContext(ctx, `SELECT user_id,cwd,transport FROM sessions WHERE id=?`, plan.Target.Session).Scan(&oldOwner, &oldCwd, &transport)
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if exists && (oldOwner != owner || oldCwd != plan.Target.Cwd || transport != "local") {
		return nil, fmt.Errorf("WorkBuddy session ID already belongs to another task")
	}
	if exists {
		b, err := os.ReadFile(plan.Target.Path)
		if err != nil || string(b) != plan.Content {
			return nil, fmt.Errorf("WorkBuddy target already contains different history")
		}
	}
	if err := shared.ApplyAttachments(plan); err != nil {
		return nil, err
	}
	result, err := shared.ApplyFilePlan(plan)
	if err != nil {
		return nil, err
	}
	if !exists {
		now := time.Now().UnixMilli()
		_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,cwd,user_id,title,custom_title,status,created_at,updated_at,last_activity_at,is_playground,mode,permission_mode,transport,unread) VALUES(?,?,?,?,?,'completed',?,?,?,0,'craft','default','local',0)`, plan.Target.Session, plan.Target.Cwd, owner, plan.Target.Title, plan.Target.Title, now, now, now)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	c.files.Set(plan.Target.Session, plan.Target.Path)
	result["uri"] = c.harness + "://chat/" + plan.Target.Session
	return result, nil
}
