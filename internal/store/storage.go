package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Storage says where the data lives and whether it outlives the container.
type Storage struct {
	Path string `json:"path"`
	// Persistent is false only inside a container whose data directory is not
	// a mounted volume: the data is then lost when the container is removed
	// or recreated for a new image (a restart keeps it).
	Persistent bool `json:"persistent"`
}

// CheckWritable fails early, with the fix, when the data directory cannot be
// written: a host folder mounted as /data is often owned by root, while the
// image runs as the unprivileged user 65532.
func CheckWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cannot write to %s (the container runs as user %d): give that user the folder, "+
				"e.g. `sudo chown -R 65532:65532 <host folder>`, or use a named volume (-v comporta-data:/data): %w",
				dir, os.Getuid(), err)
		}
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// DetectStorage reports whether dir survives the container. Outside a
// container the data is on the machine's disk, so it is persistent.
func DetectStorage(dir string) Storage {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	st := Storage{Path: abs, Persistent: true}
	if !inContainer() {
		return st
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return st // unknown: do not warn without evidence
	}
	defer func() { _ = f.Close() }()
	st.Persistent = mountedAt(f, abs)
	return st
}

func inContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// mountedAt reads /proc/self/mountinfo and reports whether dir, or a folder
// above it other than /, is a mount point.
func mountedAt(mountinfo io.Reader, dir string) bool {
	sc := bufio.NewScanner(mountinfo)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		// The 5th field is the mount point, with spaces written as \040.
		point := strings.ReplaceAll(fields[4], `\040`, " ")
		if point == "/" {
			continue
		}
		if dir == point || strings.HasPrefix(dir, strings.TrimSuffix(point, "/")+"/") {
			return true
		}
	}
	return false
}
