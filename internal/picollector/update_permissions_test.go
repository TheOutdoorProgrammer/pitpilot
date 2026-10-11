package picollector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUpdaterReleasePermissionsUnderPrivateUmask(t *testing.T) {
	if os.Getenv("PITPILOT_TEST_PRIVATE_UMASK") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-test.run=^TestUpdaterReleasePermissionsUnderPrivateUmask$", "-test.v")
		cmd.Env = append(os.Environ(), "PITPILOT_TEST_PRIVATE_UMASK=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated restrictive-umask check: %v\n%s", err, output)
		}
		return
	}
	// Umask is process-global, so only this dedicated subprocess changes it.
	unix.Umask(0077)
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "repair-existing"
		}
		t.Run(name, func(t *testing.T) {
			f := newUpdateFixture(t)
			dir := filepath.Join(f.u.InstallDir, "releases", f.m.Version+"-"+f.m.SHA256[:16])
			if existing {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.u.Check(context.Background(), f.c); err != nil {
				t.Fatal(err)
			}
			requireUpdateMode(t, filepath.Dir(dir), 0755)
			requireUpdateMode(t, dir, 0755)
			requireUpdateMode(t, filepath.Join(dir, "picollector"), 0755)
			requireUpdateMode(t, f.u.StateDir, 0700)
			requireUpdateMode(t, filepath.Join(f.u.StateDir, "state.json"), 0600)
			requireUpdateMode(t, filepath.Join(f.u.StateDir, "lock"), 0600)
			requireUpdateMode(t, f.c.StateDirectory, 0700)
			requireUpdateMode(t, filepath.Join(f.c.StateDirectory, "identity.json"), 0600)
			requireUpdateMode(t, filepath.Join(f.c.StateDirectory, "queue-sentinel"), 0600)
			if f.service.starts != 1 || f.service.stops != 1 || f.state(t).Pending != "" {
				t.Fatal("permission repair did not complete normal healthy activation")
			}
		})
	}
	t.Run("unprivileged-execution", func(t *testing.T) {
		f := newUpdateFixture(t)
		base, err := os.MkdirTemp("/tmp", "pitpilot-reader-access-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(base) })
		if err := os.Chmod(base, 0755); err != nil {
			t.Fatal(err)
		}
		f.u.InstallDir = base
		f.binary = []byte("#!/bin/sh\nexit 0\n")
		f.m = manifestFixture(f.binary)
		target, err := f.u.download(context.Background(), f.m)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(target)
		if os.Geteuid() == 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, NoSetGroups: true}}
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unprivileged reader cannot execute the verified target: %v\n%s", err, output)
		}
		requireUpdateMode(t, filepath.Dir(target), 0755)
		requireUpdateMode(t, filepath.Join(base, "releases"), 0755)
	})
}

func TestUpdaterReleasePermissionsRejectUnsafeDirectories(t *testing.T) {
	for _, component := range []string{"install", "releases", "release"} {
		t.Run(component+"-symlink", func(t *testing.T) {
			f := newUpdateFixture(t)
			victim := t.TempDir()
			if err := os.Chmod(victim, 0700); err != nil {
				t.Fatal(err)
			}
			var link string
			switch component {
			case "install":
				link = filepath.Join(t.TempDir(), "install")
				f.u.InstallDir = link
			case "releases":
				f.u.InstallDir = t.TempDir()
				link = filepath.Join(f.u.InstallDir, "releases")
			case "release":
				link = filepath.Join(f.u.InstallDir, "releases", f.m.Version+"-"+f.m.SHA256[:16])
			}
			if err := os.Symlink(victim, link); err != nil {
				t.Fatal(err)
			}
			if _, err := f.u.download(context.Background(), f.m); err == nil {
				t.Fatal("symlinked update directory accepted")
			}
			requireUpdateMode(t, victim, 0700)
			entries, err := os.ReadDir(victim)
			if err != nil || len(entries) != 0 || f.fetches != 0 {
				t.Fatal("unsafe path modified or download started before validation", err)
			}
		})
	}
	t.Run("writable-release", func(t *testing.T) {
		f := newUpdateFixture(t)
		dir := filepath.Join(f.u.InstallDir, "releases", f.m.Version+"-"+f.m.SHA256[:16])
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0777); err != nil {
			t.Fatal(err)
		}
		if _, err := f.u.download(context.Background(), f.m); err == nil {
			t.Fatal("untrusted writable release accepted")
		}
		requireUpdateMode(t, dir, 0777)
		if f.fetches != 0 {
			t.Fatal("download started before directory validation")
		}
	})
}

func requireUpdateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions=%04o, want %04o", filepath.Base(path), got, want)
	}
}
