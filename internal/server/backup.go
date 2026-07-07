package server

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type backupManifest struct {
	CreatedAt string          `json:"createdAt"`
	Version   string          `json:"version"`
	Nodes     []NodeInventory `json:"nodes"`
	Files     []string        `json:"files"`
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	name := "rookery-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	manifest := backupManifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), Version: s.version, Nodes: s.nodeInventory(r)}
	if s.users != nil && s.users.Path() != "" {
		if data, err := os.ReadFile(s.users.Path()); err == nil {
			p := "rookery/rookery.db"
			if err := writeTarFile(tw, p, data, 0o600); err == nil {
				manifest.Files = append(manifest.Files, p)
			}
		}
	}
	for _, area := range s.areas {
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
