package server

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rookerylabs/rookery/internal/appdb"
	"github.com/rookerylabs/rookery/internal/quadlet"
)

type backupManifest struct {
	CreatedAt string            `json:"createdAt"`
	Version   string            `json:"version"`
	Nodes     []NodeInventory   `json:"nodes"`
	Files     []string          `json:"files"`
	SHA256    map[string]string `json:"sha256,omitempty"`
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	name := "rookery-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	manifest := backupManifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), Version: s.version, Nodes: s.nodeInventory(r), SHA256: map[string]string{}}
	if s.users != nil && s.users.Path() != "" {
		if data, err := os.ReadFile(s.users.Path()); err == nil {
			p := "rookery/rookery.db"
			if err := writeTarFile(tw, p, data, 0o600); err == nil {
				manifest.Files = append(manifest.Files, p)
				manifest.SHA256[p] = sha256Hex(data)
			}
		}
	}
	for _, area := range s.areasSnapshot() {
		found, err := discoverArea(r.Context(), area)
		if err != nil {
			continue
		}
		for _, d := range found {
			if d.data == nil {
				continue
			}
			p := path.Join("quadlets", safePathPart(area.Label), d.unit.Name)
			if err := writeTarFile(tw, p, d.data, 0o644); err == nil {
				manifest.Files = append(manifest.Files, p)
				manifest.SHA256[p] = sha256Hex(d.data)
			}
		}
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	_ = writeTarFile(tw, "manifest.json", append(data, '\n'), 0o644)
	s.audit(r, "backup.download", "backup", map[string]any{"files": len(manifest.Files)})
}

func writeTarFile(tw *tar.Writer, name string, data []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

type restorePreview struct {
	Path   string `json:"path"`
	Scope  string `json:"scope,omitempty"`
	Name   string `json:"name,omitempty"`
	Action string `json:"action"`
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	dryRun := r.URL.Query().Get("dryRun") == "1"
	files, manifest, err := readBackupArchive(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	changes, err := s.restoreChanges(r, files, manifest)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if dryRun {
		writeJSON(w, http.StatusOK, map[string]any{"dryRun": true, "changes": changes})
		return
	}
	if data, ok := files["rookery/rookery.db"]; ok && s.users != nil {
		if err := restoreAppDB(s.users.DB(), data); err != nil {
			httpError(w, http.StatusInternalServerError, "restore rookery.db: "+err.Error())
			return
		}
	}
	for p, data := range files {
		if !strings.HasPrefix(p, "quadlets/") {
			continue
		}
		parts := strings.Split(p, "/")
		area, ok := s.area(parts[1])
		if !ok {
			httpError(w, http.StatusBadRequest, "backup references unknown scope "+parts[1])
			return
		}
		if area.ViaAgent() {
			httpError(w, http.StatusBadRequest, "restoring to agent scopes is not supported yet")
			return
		}
		name := parts[2]
		target := joinUnitPath(area, area.Dirs[0], name)
		_, warnings, saved, err := s.applySave(r, area, name, target, string(data), false, "rookery: restore "+name)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !saved {
			httpError(w, http.StatusUnprocessableEntity, "restored "+name+" did not validate")
			return
		}
		if len(warnings) > 0 {
			s.audit(r, "restore.warning", area.Label+"/"+name, map[string]any{"warnings": warnings})
		}
	}
	s.audit(r, "backup.restore", "backup", map[string]any{"files": len(files), "changes": len(changes)})
	writeJSON(w, http.StatusOK, map[string]any{"restored": true, "changes": changes})
}

func readBackupArchive(src io.Reader) (map[string][]byte, backupManifest, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, backupManifest{}, fmt.Errorf("backup must be a tar.gz archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, backupManifest{}, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		clean := path.Clean(h.Name)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return nil, backupManifest{}, fmt.Errorf("unsafe archive path %q", h.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			return nil, backupManifest{}, err
		}
		files[clean] = data
	}
	var manifest backupManifest
	data, ok := files["manifest.json"]
	if !ok {
		return nil, backupManifest{}, fmt.Errorf("manifest.json missing")
	}
	delete(files, "manifest.json")
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, backupManifest{}, fmt.Errorf("manifest.json: %w", err)
	}
	listed := map[string]bool{}
	for _, p := range manifest.Files {
		p = path.Clean(p)
		listed[p] = true
		data, ok := files[p]
		if !ok {
			return nil, backupManifest{}, fmt.Errorf("manifest lists missing file %s", p)
		}
		if manifest.SHA256 != nil && manifest.SHA256[p] != "" && manifest.SHA256[p] != sha256Hex(data) {
			return nil, backupManifest{}, fmt.Errorf("checksum mismatch for %s", p)
		}
	}
	for p := range files {
		if !listed[p] {
			return nil, backupManifest{}, fmt.Errorf("archive contains file not listed in manifest: %s", p)
		}
	}
	return files, manifest, nil
}

func (s *Server) restoreChanges(r *http.Request, files map[string][]byte, manifest backupManifest) ([]restorePreview, error) {
	_ = manifest
	var changes []restorePreview
	if _, ok := files["rookery/rookery.db"]; ok {
		changes = append(changes, restorePreview{Path: "rookery/rookery.db", Action: "overwrite"})
	}
	for p, data := range files {
		if !strings.HasPrefix(p, "quadlets/") {
			continue
		}
		parts := strings.Split(p, "/")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid quadlet path %s", p)
		}
		area, ok := s.area(parts[1])
		if !ok {
			return nil, fmt.Errorf("backup references unknown scope %s", parts[1])
		}
		name := parts[2]
		if err := quadlet.CheckName(name); err != nil {
			return nil, err
		}
		if area.ViaAgent() {
			changes = append(changes, restorePreview{Path: p, Scope: area.Label, Name: name, Action: "unsupported"})
			continue
		}
		action := "add"
		target := joinUnitPath(area, area.Dirs[0], name)
		if cur, err := areaReadFile(r.Context(), area, target); err == nil {
			if string(cur) == string(data) {
				action = "unchanged"
			} else {
				action = "overwrite"
			}
		}
		changes = append(changes, restorePreview{Path: p, Scope: area.Label, Name: name, Action: action})
	}
	return changes, nil
}

func restoreAppDB(current *sql.DB, data []byte) error {
	tmp, err := os.CreateTemp("", "rookery-restore-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	src, err := appdb.Open(tmpPath)
	if err != nil {
		return err
	}
	defer src.Close()
	tables := []string{"users", "settings", "setting_overrides", "node_metadata", "policy_waivers", "audit_events"}
	tx, err := current.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, _ = tx.Exec(`DELETE FROM sessions`)
	for _, table := range tables {
		if !tableExists(src, table) || !tableExists(current, table) {
			continue
		}
		rows, err := src.Query(`SELECT * FROM ` + table)
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			rows.Close()
			return err
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(cols)), ",")
		insert := `INSERT INTO ` + table + `(` + strings.Join(cols, ",") + `) VALUES (` + placeholders + `)`
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return err
			}
			if _, err := tx.Exec(insert, vals...); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return tx.Commit()
}

func tableExists(db *sql.DB, table string) bool {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
	return err == nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func safePathPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unnamed"
	}
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return fmt.Sprintf("%s", s)
}
