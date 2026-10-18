package zcode

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"koto/internal/core"
	"koto/internal/engine"
)

// stageAttachments owns only files created by this delivery. A failed SQLite
// transaction removes those files, never an existing source or another copy.
func stageAttachments(p *engine.DeliveryPlan) (func(), error) {
	created := []string{}
	cleanup := func() {
		for i := len(created) - 1; i >= 0; i-- {
			path := created[i]
			_ = os.Remove(path)
			_ = os.Remove(filepath.Dir(path))
			_ = os.Remove(filepath.Dir(filepath.Dir(path)))
		}
	}
	for _, v := range core.Arr(p.Extra["attachments"]) {
		a := core.Obj(v)
		path := core.S(a["path"])
		var b []byte
		switch d := a["data"].(type) {
		case []byte:
			b = d
		case string:
			b = core.Base64(d)
		default:
			cleanup()
			return nil, fmt.Errorf("invalid ZCode attachment payload")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			cleanup()
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			old, e := os.ReadFile(path)
			if e == nil && bytes.Equal(old, b) {
				continue
			}
			cleanup()
			return nil, fmt.Errorf("ZCode attachment already exists with different content")
		}
		if err != nil {
			cleanup()
			return nil, err
		}
		created = append(created, path)
		_, err = f.Write(b)
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			cleanup()
			return nil, err
		}
	}
	return cleanup, nil
}
