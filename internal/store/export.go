package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// notifyExportVersion is the versioned JSON schema of a notification export
// (the persist ComponentNotifyExport payload).
const notifyExportVersion = 1

// MetaNotifyCheckpoint is the metadata key recording the checkpoint id of the
// last committed notification export. ImportNotifications accepts only the
// export matching this committed id (ADR-0005: an explicit restore imports
// only the matching committed notification checkpoint).
const MetaNotifyCheckpoint = "notify_checkpoint_id"

// notifyExport is the versioned export document. The field set is closed:
// strict decoding rejects unknown fields, so a crafted export smuggling trust
// fields (epochs, grants, audit) fails before any write.
type notifyExport struct {
	Version       int                  `json:"version"`
	CheckpointID  string               `json:"checkpoint_id"`
	Notifications []notificationExport `json:"notifications"`
}

// notificationExport is one exported notification including its read state.
type notificationExport struct {
	ID        string `json:"id"`
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedMS int64  `json:"created_ms"`
	ReadAtMS  int64  `json:"read_at_ms"`
}

// ExportNotifications serializes the full notification/read state as the
// versioned notify_export component of a snapshot generation and records
// checkpointID as the committed notification checkpoint in metadata — the id
// a later ImportNotifications must match. Read and record happen in one
// transaction so the export bytes and the committed id cannot diverge.
func (s *Store) ExportNotifications(checkpointID string) ([]byte, error) {
	doc := notifyExport{
		Version:       notifyExportVersion,
		CheckpointID:  checkpointID,
		Notifications: []notificationExport{},
	}
	err := s.inTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT id, session, workspace, pane, kind, title, body, created_ms, read_at_ms
			FROM notifications ORDER BY created_ms, id`)
		if err != nil {
			return fmt.Errorf("store: export notifications: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var n notificationExport
			if err := rows.Scan(&n.ID, &n.Session, &n.Workspace, &n.Pane,
				&n.Kind, &n.Title, &n.Body, &n.CreatedMS, &n.ReadAtMS); err != nil {
				return fmt.Errorf("store: export scan: %w", err)
			}
			doc.Notifications = append(doc.Notifications, n)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			MetaNotifyCheckpoint, checkpointID); err != nil {
			return fmt.Errorf("store: record notify checkpoint: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("store: marshal notify export: %w", err)
	}
	return out, nil
}

// ImportNotifications replaces the notification/read state with a previously
// exported document. Enforced fail-closed, per ADR-0005 ("a snapshot restore
// may import ONLY its notification/read export; it can never restore,
// decrease, or reactivate security state"):
//
//   - The payload is decoded strictly: unknown fields (the vector for forged
//     trust state), a wrong version, or malformed JSON fail with
//     ErrImportInvalid before any write.
//   - checkpointID must equal both the document's embedded id and the
//     committed checkpoint recorded in metadata; any mismatch (including a
//     database that never committed an export) fails with
//     ErrCheckpointMismatch.
//   - The transaction touches ONLY the notifications table. Projects, grants,
//     and audit are structurally out of reach of an import.
func (s *Store) ImportNotifications(checkpointID string, data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc notifyExport
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("%w: %v", ErrImportInvalid, err)
	}
	// Reject trailing data after the document.
	if dec.More() {
		return fmt.Errorf("%w: trailing data after export document", ErrImportInvalid)
	}
	if doc.Version != notifyExportVersion {
		return fmt.Errorf("%w: unsupported export version %d", ErrImportInvalid, doc.Version)
	}
	if doc.CheckpointID != checkpointID {
		return fmt.Errorf("%w: document checkpoint %q, requested %q",
			ErrCheckpointMismatch, doc.CheckpointID, checkpointID)
	}
	return s.inTx(func(tx *sql.Tx) error {
		var committed string
		err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`,
			MetaNotifyCheckpoint).Scan(&committed)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no committed notification checkpoint recorded",
				ErrCheckpointMismatch)
		}
		if err != nil {
			return fmt.Errorf("store: read notify checkpoint: %w", err)
		}
		if committed != checkpointID {
			return fmt.Errorf("%w: committed checkpoint %q, requested %q",
				ErrCheckpointMismatch, committed, checkpointID)
		}
		if _, err := tx.Exec(`DELETE FROM notifications`); err != nil {
			return fmt.Errorf("store: clear notifications: %w", err)
		}
		for _, n := range doc.Notifications {
			if _, err := tx.Exec(`
				INSERT INTO notifications (id, session, workspace, pane, kind, title, body, created_ms, read_at_ms)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				n.ID, n.Session, n.Workspace, n.Pane, n.Kind, n.Title,
				n.Body, n.CreatedMS, n.ReadAtMS); err != nil {
				return fmt.Errorf("store: import notification %q: %w", n.ID, err)
			}
		}
		return nil
	})
}
