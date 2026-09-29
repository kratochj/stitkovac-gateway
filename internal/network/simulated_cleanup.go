package network

import (
	"os"
	"path/filepath"
)

func cleanupSimulation(dir string, keep []string) error {
	allowed := map[string]bool{}
	for _, id := range keep {
		allowed[profilePath(dir, id)] = true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "gateway-*.nmconnection"))
	for _, f := range files {
		if !allowed[f] {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	}
	return nil
}
