package safety

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSessionLockCancellationAndIndependentAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	a, b := &SessionLock{}, &SessionLock{}
	if err := a.Acquire(path, 0); err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.AcquireContext(ctx, path, 30, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := b.Acquire(path+"-other", 0); err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	if err := a.Acquire(path+"-other", 0); err == nil {
		t.Fatal("one lock instance silently covered two sessions")
	}
}
func TestSessionLockReadonlyDoesNotCreateOrTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	lock := &SessionLock{}
	if err := lock.AcquireContext(context.Background(), path, 0, true); err == nil {
		t.Fatal("missing sidecar accepted")
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("read-only created sidecar")
	}
	if err := os.WriteFile(path+".lock", []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := lock.Acquire(path, 0); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	other := &SessionLock{}
	if err := other.Acquire(path, 0); err == nil {
		t.Fatal("contender acquired")
	}
	// Windows locks are mandatory for other handles, including in this process.
	lock.Release()
	b, err := os.ReadFile(path + ".lock")
	if err != nil || string(b) != "owner" {
		t.Fatalf("lock contents changed: %v", err)
	}
}

func TestSessionLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("TGCTL_TEST_LOCK_PATH"); path != "" {
		lock := &SessionLock{}
		err := lock.Acquire(path, 0)
		if err == nil {
			lock.Release()
			os.Exit(10)
		}
		var held *SessionLocked
		if !errors.As(err, &held) {
			os.Exit(11)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "session")
	lock := &SessionLock{}
	if err := lock.Acquire(path, 0); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TGCTL_TEST_LOCK_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child bypassed lock: %v %s", err, out)
	}
}
func TestSessionLockRejectsHardLinkAndSidecarSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix alias checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+"-alias"); err != nil {
		t.Fatal(err)
	}
	lock := &SessionLock{}
	if err := lock.Acquire(path, 0); err == nil {
		lock.Release()
		t.Fatal("hard-link alias accepted")
	}
	os.Remove(path + "-alias")
	if err := os.Symlink(path, path+".lock"); err != nil {
		t.Fatal(err)
	}
	if err := lock.Acquire(path, 0); err == nil {
		lock.Release()
		t.Fatal("sidecar symlink accepted")
	}
}
