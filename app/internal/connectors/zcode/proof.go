package zcode

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"time"
)

// RunningToolSessions reads only recent native running shell commands. It never
// searches message bodies or infers identity from a workspace or recency alone.
func (c *connector) RunningToolSessions(ctx context.Context, invocation *regexp.Regexp) ([]string, error) {
	if invocation == nil {
		return nil, fmt.Errorf("missing ZCode invocation proof")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db, err := c.open(ctx, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT p.session_id,json_extract(p.data,'$.state.input.command') FROM part p JOIN session s ON s.id=p.session_id WHERE s.time_archived IS NULL AND p.time_updated>=? AND json_extract(p.data,'$.type')='tool' AND json_extract(p.data,'$.tool') IN ('Bash','bash','shell') AND json_extract(p.data,'$.state.status')='running'`, time.Now().Add(-5*time.Minute).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var command sql.NullString
		if err = rows.Scan(&id, &command); err != nil {
			return nil, err
		}
		if command.Valid && invocation.MatchString(command.String) && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, rows.Err()
}
