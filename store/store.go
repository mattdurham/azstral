// Package store provides SQLite-backed storage for specs, notes, tests, and benchmarks.
// SPEC-008: Spec identifiers are stored in SQLite with namespace support.
// SPEC-009: A spec ID (e.g. SPEC-001) is globally unique but applies at a namespace level.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database for spec storage.
type Store struct {
	db *sql.DB
}

// Spec represents a stored spec/note/test/benchmark entry.
type Spec struct {
	ID        string    `json:"id"`        // e.g. "SPEC-001"
	Kind      string    `json:"kind"`      // SPEC, NOTE, TEST, BENCH
	Namespace string    `json:"namespace"` // e.g. "io", "graph", "" for root
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SpecLink associates a spec with a graph node.
type SpecLink struct {
	SpecID string `json:"spec_id"`
	NodeID string `json:"node_id"`
}

// Open opens or creates a SQLite database at the given path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS specs (
			id         TEXT PRIMARY KEY,
			kind       TEXT NOT NULL CHECK(kind IN ('SPEC', 'NOTE', 'TEST', 'BENCH')),
			namespace  TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL,
			body       TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_specs_kind ON specs(kind);
		CREATE INDEX IF NOT EXISTS idx_specs_namespace ON specs(namespace);

		CREATE TABLE IF NOT EXISTS spec_links (
			spec_id TEXT NOT NULL REFERENCES specs(id),
			node_id TEXT NOT NULL,
			PRIMARY KEY (spec_id, node_id)
		);

		CREATE INDEX IF NOT EXISTS idx_spec_links_node ON spec_links(node_id);
	`)
	return err
}

// CreateSpec inserts a new spec. Returns error if ID already exists.
func (s *Store) CreateSpec(spec *Spec) error {
	now := time.Now().UTC()
	spec.CreatedAt = now
	spec.UpdatedAt = now
	_, err := s.db.Exec(
		`INSERT INTO specs (id, kind, namespace, title, body, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		spec.ID, spec.Kind, spec.Namespace, spec.Title, spec.Body,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("create spec %s: %w", spec.ID, err)
	}
	return nil
}

// UpdateSpec updates an existing spec's title and body.
func (s *Store) UpdateSpec(id, title, body string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`UPDATE specs SET title = ?, body = ?, updated_at = ? WHERE id = ?`,
		title, body, now, id,
	)
	if err != nil {
		return fmt.Errorf("update spec %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("spec %s not found", id)
	}
	return nil
}

// GetSpec returns a spec by ID.
func (s *Store) GetSpec(id string) (*Spec, error) {
	row := s.db.QueryRow(
		`SELECT id, kind, namespace, title, body, created_at, updated_at FROM specs WHERE id = ?`, id,
	)
	return scanSpec(row)
}

// ListSpecs returns all specs, optionally filtered by kind and/or namespace.
func (s *Store) ListSpecs(kind, namespace string) ([]*Spec, error) {
	query := `SELECT id, kind, namespace, title, body, created_at, updated_at FROM specs WHERE 1=1`
	var args []any
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	if namespace != "" {
		query += ` AND namespace = ?`
		args = append(args, namespace)
	}
	query += ` ORDER BY id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []*Spec
	for rows.Next() {
		var sp Spec
		var created, updated string
		if err := rows.Scan(&sp.ID, &sp.Kind, &sp.Namespace, &sp.Title, &sp.Body, &created, &updated); err != nil {
			return nil, err
		}
		sp.CreatedAt, _ = time.Parse(time.RFC3339, created)
		sp.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		specs = append(specs, &sp)
	}
	return specs, rows.Err()
}

// LinkSpec associates a spec with a graph node ID.
func (s *Store) LinkSpec(specID, nodeID string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO spec_links (spec_id, node_id) VALUES (?, ?)`,
		specID, nodeID,
	)
	return err
}

// UnlinkSpec removes a spec-node association.
func (s *Store) UnlinkSpec(specID, nodeID string) error {
	_, err := s.db.Exec(
		`DELETE FROM spec_links WHERE spec_id = ? AND node_id = ?`,
		specID, nodeID,
	)
	return err
}

// GetLinks returns all node IDs linked to a spec.
func (s *Store) GetLinks(specID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT node_id FROM spec_links WHERE spec_id = ? ORDER BY node_id`, specID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetSpecsForNode returns all specs linked to a given node ID.
func (s *Store) GetSpecsForNode(nodeID string) ([]*Spec, error) {
	rows, err := s.db.Query(
		`SELECT s.id, s.kind, s.namespace, s.title, s.body, s.created_at, s.updated_at
		 FROM specs s JOIN spec_links l ON s.id = l.spec_id
		 WHERE l.node_id = ? ORDER BY s.id`, nodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []*Spec
	for rows.Next() {
		var sp Spec
		var created, updated string
		if err := rows.Scan(&sp.ID, &sp.Kind, &sp.Namespace, &sp.Title, &sp.Body, &created, &updated); err != nil {
			return nil, err
		}
		sp.CreatedAt, _ = time.Parse(time.RFC3339, created)
		sp.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		specs = append(specs, &sp)
	}
	return specs, rows.Err()
}

// DeleteSpec removes a spec and all its links.
func (s *Store) DeleteSpec(id string) error {
	_, err := s.db.Exec(`DELETE FROM spec_links WHERE spec_id = ?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM specs WHERE id = ?`, id)
	return err
}

func scanSpec(row *sql.Row) (*Spec, error) {
	var sp Spec
	var created, updated string
	if err := row.Scan(&sp.ID, &sp.Kind, &sp.Namespace, &sp.Title, &sp.Body, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("spec not found")
		}
		return nil, err
	}
	sp.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sp.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &sp, nil
}
