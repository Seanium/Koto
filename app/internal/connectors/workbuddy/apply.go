package workbuddy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
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
func (c *connector) OpenBy(s engine.SessionRef) string {
	db, err := c.open(false)
	if err != nil {
		return "cannot verify WorkBuddy task state"
	}
	defer db.Close()
	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id=?`, s.Session).Scan(&status); err != nil {
		return ""
	}
	switch strings.ToLower(status) {
	case "working", "active", "running", "pending":
		return "WorkBuddy task is running"
	}
	return ""
}
func (c *connector) Archive(ctx context.Context, s engine.SessionRef) (core.JsonObject, error) {
	db, err := c.open(true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var owner string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id=? AND transport='local'`, s.Session).Scan(&owner); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	r, err := db.ExecContext(ctx, `UPDATE sessions SET deleted_at=?,updated_at=? WHERE id=? AND user_id=? AND transport='local' AND deleted_at IS NULL AND lower(status) NOT IN ('working','active','running','pending')`, now, now, s.Session, owner)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return nil, fmt.Errorf("WorkBuddy task is missing, archived or running")
	}
	return core.JsonObject{"deletedAt": now, "owner": owner}, nil
}
func (c *connector) Unarchive(ctx context.Context, s engine.SessionRef, receipt core.JsonObject) error {
	db, err := c.open(true)
	if err != nil {
		return err
	}
	defer db.Close()
	var owner string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id=? AND transport='local'`, s.Session).Scan(&owner); err != nil {
		return err
	}
	if owner != core.S(receipt["owner"]) {
		return fmt.Errorf("WorkBuddy account changed since archive")
	}
	r, err := db.ExecContext(ctx, `UPDATE sessions SET deleted_at=NULL,updated_at=? WHERE id=? AND user_id=? AND deleted_at=?`, time.Now().UnixMilli(), s.Session, owner, receipt["deletedAt"])
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("WorkBuddy task changed since archive")
	}
	return nil
}
