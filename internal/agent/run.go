package agent

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// Run performs one pass: fetch, fall back to cache if that fails, apply every
// consumer, then promote the cache only if everything succeeded.
func Run(ctx context.Context, cfg *Config, log Logger) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.StateDir, 0o700); err != nil {
		return err
	}

	// A manual run while the timer fires is the obvious debugging move; serialise it
	// rather than letting two passes interleave their writes.
	unlock, held, err := lock(cfg.LockPath())
	if err != nil {
		return err
	}
	if !held {
		log.Infof("another run holds the lock, exiting")
		return nil
	}
	defer unlock()

	fetched, fetchErr := NewFetcher(cfg, log).Fetch(ctx)
	if fetchErr != nil {
		log.Warnf("fetch failed, falling back to cache: %v", fetchErr)
	}

	values := fetched
	if values == nil {
		cached, err := os.ReadFile(cfg.CachePath())
		if err != nil {
			return fmt.Errorf("no usable payload and no cache: %w", err)
		}
		if values, err = Decode(cached); err != nil {
			return fmt.Errorf("cache is unusable: %w", err)
		}
		log.Infof("using cached payload (%d variables)", len(values))
	} else {
		log.Infof("fetched %d variables", len(values))
	}

	// Variables terraform owns: values derived from resources it manages, plus
	// non-secret ones the agent still has to hand to compose because systemd units do
	// not read /etc/environment. Fetched values win, so a collision cannot silently
	// shadow what the secret store holds.
	tfEnv, err := ReadKeyValueFile(cfg.TFEnvFile)
	if err != nil {
		return err
	}
	merged := Merge(Values(tfEnv), values)

	applier, err := NewApplier(cfg, log)
	if err != nil {
		return err
	}

	applier.Compose(merged)
	applier.Alloy(merged)
	applier.Files(merged)

	if err := applier.Save(); err != nil {
		return err
	}

	failures := applier.Failures()

	// The cache is promoted last and only on a clean pass: a payload that cannot be
	// applied must not become the fallback, or the failure replays on every run and
	// outlives the fix upstream.
	if fetched != nil && len(failures) == 0 {
		encoded, err := values.JSON()
		if err != nil {
			return err
		}
		if err := WriteFileAtomic(cfg.CachePath(), encoded, 0o600, ""); err != nil {
			return err
		}
	}

	if len(failures) > 0 {
		for _, failure := range failures {
			log.Warnf("%s", failure)
		}
		return fmt.Errorf("%d consumer(s) failed, will retry next run", len(failures))
	}

	log.Infof("done")
	return nil
}

func lock(path string) (func(), bool, error) {
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = handle.Close()
		return nil, false, nil
	}
	return func() {
		_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		_ = handle.Close()
	}, true, nil
}
