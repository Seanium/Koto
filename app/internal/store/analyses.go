package store

import (
	"encoding/json"
	"fmt"
	"koto/internal/core"
)

// AnalysisCollection stores only a name and native references. Graphs and diffs are computed on demand.
type AnalysisCollection struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Sessions []string `json:"sessions"`
}

func (s *Store) Analyses() ([]AnalysisCollection, error) {
	rows, err := s.db.Query("SELECT id,name,sessions FROM analyses ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnalysisCollection{}
	for rows.Next() {
		var a AnalysisCollection
		var refs string
		if err = rows.Scan(&a.ID, &a.Name, &refs); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(refs), &a.Sessions); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) SaveAnalysis(a AnalysisCollection, create bool) error {
	if create {
		_, err := s.db.Exec("INSERT INTO analyses(id,name,sessions) VALUES(?,?,?)", a.ID, a.Name, core.ToJSON(a.Sessions))
		return err
	}
	result, err := s.db.Exec("UPDATE analyses SET name=?,sessions=? WHERE id=?", a.Name, core.ToJSON(a.Sessions), a.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("analysis collection no longer exists")
	}
	return nil
}
func (s *Store) DeleteAnalysis(id string) error {
	_, err := s.db.Exec("DELETE FROM analyses WHERE id=?", id)
	return err
}
